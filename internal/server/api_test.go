package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nikenesx/aura-server/internal/appointments"
	"github.com/nikenesx/aura-server/internal/audio"
	"github.com/nikenesx/aura-server/internal/auth"
	"github.com/nikenesx/aura-server/internal/card"
	"github.com/nikenesx/aura-server/internal/health"
	"github.com/nikenesx/aura-server/internal/jobs"
	"github.com/nikenesx/aura-server/internal/store"
)

type fakeRunner struct{}

func (fakeRunner) Transcribe(context.Context, string, io.Reader) (jobs.Result, error) {
	return jobs.Result{
		Text:     "Здравствуйте. Кошку зовут Муся.",
		Phrases:  []jobs.Phrase{{Text: "Здравствуйте.", Speaker: "speaker_0"}, {Text: "Кошку зовут Муся.", Speaker: "speaker_1"}},
		Duration: 3,
	}, nil
}

type fakeCards struct{}

func (fakeCards) Roles(context.Context, []card.Line) (map[string]string, error) {
	return map[string]string{"speaker_0": card.RoleDoctor, "speaker_1": card.RoleOwner}, nil
}

func (fakeCards) Make(context.Context, []card.Line, map[string]string, func(float64)) (*card.Card, error) {
	return card.Decode([]byte(`{"template":"vet_appointment_v1","roles":{"speaker_0":"doctor","speaker_1":"owner"},
		"sections":[{"fields":[{"key":"pet_name","value":"Муся","sources":[1]}]}]}`))
}

type api struct {
	t   *testing.T
	url string
	db  *store.DB
}

func newAPI(t *testing.T, maxUpload int64) *api {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := store.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rec, err := audio.NewDisk(filepath.Join(dir, "audio"))
	if err != nil {
		t.Fatal(err)
	}
	authService, err := auth.NewService(db, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authService.Bootstrap(ctx, "admin", "1234"); err != nil {
		t.Fatal(err)
	}

	worker := jobs.NewWorker(db, rec, fakeRunner{})
	worker.EnableCards(fakeCards{})
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	h := appointments.NewHandler(db, rec, worker)
	h.MaxUpload = maxUpload

	srv := httptest.NewServer(NewRouter(&health.Handler{}, authService, h))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL, db: db}
}

func (a *api) do(method, path, token string, body io.Reader, header map[string]string) (*http.Response, []byte) {
	a.t.Helper()
	req, err := http.NewRequest(method, a.url+path, body)
	if err != nil {
		a.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func (a *api) login(user, password string) (string, int) {
	a.t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": password})
	resp, data := a.do(http.MethodPost, "/v1/auth/login", "", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(data, &out)
	return out.Token, resp.StatusCode
}

func (a *api) upload(token, name, content string) (*http.Response, []byte) {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", name)
	_, _ = part.Write([]byte(content))
	_ = mw.Close()
	return a.do(http.MethodPost, "/v1/appointments", token, &buf, map[string]string{"Content-Type": mw.FormDataContentType()})
}

func (a *api) waitDone(token, id string) map[string]any {
	a.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, data := a.do(http.MethodGet, "/v1/appointments/"+id, token, nil, nil)
		var got map[string]any
		_ = json.Unmarshal(data, &got)
		if got["status"] == "done" && got["card"] != nil {
			return got
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("appointment not done: %s", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAuthRequired(t *testing.T) {
	a := newAPI(t, 1<<20)
	if resp, _ := a.do(http.MethodGet, "/v1/appointments", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := a.do(http.MethodGet, "/v1/appointments", "made-up", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", resp.StatusCode)
	}
	if _, code := a.login("admin", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	if _, code := a.login("nobody", "1234"); code != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d", code)
	}
	if resp, _ := a.do(http.MethodGet, "/health", "", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("health must stay public: %d", resp.StatusCode)
	}

	token, code := a.login("admin", "1234")
	if code != http.StatusOK || token == "" {
		t.Fatalf("login: %d", code)
	}
	resp, data := a.do(http.MethodGet, "/v1/auth/me", token, nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"username":"admin"`) {
		t.Fatalf("me: %d %s", resp.StatusCode, data)
	}

	if resp, _ := a.do(http.MethodPost, "/v1/auth/logout", token, nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if resp, _ := a.do(http.MethodGet, "/v1/auth/me", token, nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token must die on logout: %d", resp.StatusCode)
	}
}

func TestAppointmentFlow(t *testing.T) {
	a := newAPI(t, 1<<20)
	token, _ := a.login("admin", "1234")

	resp, data := a.upload(token, "visit.m4a", "RIFF-audio-bytes")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload: %d %s", resp.StatusCode, data)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &created)

	got := a.waitDone(token, created.ID)
	if got["filename"] != "visit.m4a" || got["duration_sec"] != 3.0 || got["card_edited_at"] != nil {
		t.Fatalf("detail: %v", got)
	}
	if phrases := got["transcript"].(map[string]any)["phrases"].([]any); len(phrases) != 2 {
		t.Fatalf("transcript: %v", got["transcript"])
	}

	// History shows the pet from the card.
	_, data = a.do(http.MethodGet, "/v1/appointments", token, nil, nil)
	if !strings.Contains(string(data), `"pet_name":"Муся"`) {
		t.Fatalf("list: %s", data)
	}

	// The player seeks with Range requests.
	resp, data = a.do(http.MethodGet, "/v1/appointments/"+created.ID+"/audio", token, nil, map[string]string{"Range": "bytes=0-3"})
	if resp.StatusCode != http.StatusPartialContent || string(data) != "RIFF" || resp.Header.Get("Content-Type") != "audio/mp4" {
		t.Fatalf("audio range: %d %q %s", resp.StatusCode, data, resp.Header.Get("Content-Type"))
	}

	// The doctor edits the card; the edit sticks and re-renders the text.
	edit := `{"sections":[{"fields":[{"key":"pet_name","value":"Мурка","sources":[1]},{"key":"diagnosis","value":"Отит","sources":[99]}]}]}`
	resp, data = a.do(http.MethodPut, "/v1/appointments/"+created.ID+"/card", token, strings.NewReader(edit), nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "Питомец: Мурка") || !strings.Contains(string(data), "Диагноз: Отит") {
		t.Fatalf("save card: %d %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "99") {
		t.Fatalf("a source outside the transcript must be dropped: %s", data)
	}
	_, data = a.do(http.MethodGet, "/v1/appointments/"+created.ID, token, nil, nil)
	if !strings.Contains(string(data), "Мурка") || strings.Contains(string(data), `"card_edited_at":null`) {
		t.Fatalf("edit not stored: %s", data)
	}
	stored, _ := a.db.Appointment(context.Background(), created.ID, "")
	if !strings.Contains(string(stored.CardGenerated), "Муся") {
		t.Fatalf("the model's version must be kept: %s", stored.CardGenerated)
	}

	// Regenerating replaces the edit with a fresh draft.
	if resp, data := a.do(http.MethodPost, "/v1/appointments/"+created.ID+"/card/regenerate", token, nil, nil); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("regenerate: %d %s", resp.StatusCode, data)
	}
	got = a.waitDone(token, created.ID)
	if got["card_edited_at"] != nil {
		t.Fatalf("regenerated card should be the model's: %v", got)
	}
}

func TestDoctorsSeeOnlyTheirOwn(t *testing.T) {
	a := newAPI(t, 1<<20)
	admin, _ := a.login("admin", "1234")
	_, data := a.upload(admin, "visit.wav", "RIFF")
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &created)

	_ = a.db.CreateUser(context.Background(), store.User{ID: "u2", Username: "vet2", PasswordHash: mustHash(t, "pw"), DisplayName: "vet2", CreatedAt: time.Now()})
	other, code := a.login("vet2", "pw")
	if code != http.StatusOK {
		t.Fatalf("second doctor login: %d", code)
	}
	for _, path := range []string{"", "/audio"} {
		if resp, _ := a.do(http.MethodGet, "/v1/appointments/"+created.ID+path, other, nil, nil); resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s by another doctor: %d", path, resp.StatusCode)
		}
	}
	base := "/v1/appointments/" + created.ID
	if resp, _ := a.do(http.MethodPut, base+"/card", other, strings.NewReader(`{"sections":[]}`), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT card by another doctor: %d", resp.StatusCode)
	}
	if resp, _ := a.do(http.MethodPost, base+"/card/regenerate", other, nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("regenerate by another doctor: %d", resp.StatusCode)
	}
	if _, data := a.do(http.MethodGet, "/v1/appointments", other, nil, nil); !strings.Contains(string(data), `"items":[]`) {
		t.Fatalf("another doctor's history: %s", data)
	}
}

func TestUploadLimits(t *testing.T) {
	a := newAPI(t, 8)
	token, _ := a.login("admin", "1234")
	if resp, _ := a.upload(token, "big.wav", "123456789"); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d", resp.StatusCode)
	}
	if resp, _ := a.upload(token, "empty.wav", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty: %d", resp.StatusCode)
	}
	if _, data := a.do(http.MethodGet, "/v1/appointments", token, nil, nil); !strings.Contains(string(data), `"items":[]`) {
		t.Fatalf("rejected uploads must leave nothing behind: %s", data)
	}
}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(hash)
}

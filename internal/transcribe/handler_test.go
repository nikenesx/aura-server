package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nikenesx/aura-server/internal/card"
	"github.com/nikenesx/aura-server/internal/config"
	"github.com/nikenesx/aura-server/internal/jobs"
)

type fakeRunner struct {
	result jobs.Result
}

func (f fakeRunner) Transcribe(context.Context, string, []byte) (jobs.Result, error) {
	return f.result, nil
}

func TestCreateAndGetJob(t *testing.T) {
	cfg := config.Config{MaxUploadBytes: 1024}
	store := jobs.NewStore(time.Hour)
	worker := jobs.NewWorker(store, fakeRunner{result: jobs.Result{Text: "привет"}})
	worker.Start(context.Background())
	h := NewHandler(cfg, store, worker, func() bool { return true })

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "rec.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("audio-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/transcribe", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status %d body %s", rec.Code, rec.Body.String())
	}

	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.JobID == "" {
		t.Fatalf("job_id: %s", rec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := getJob(t, h, created.JobID)
		if got["status"] == "done" {
			result := got["result"].(map[string]any)
			if result["text"] != "привет" {
				t.Fatalf("result: %#v", result)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %#v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCreateUnavailable(t *testing.T) {
	h := NewHandler(config.Config{MaxUploadBytes: 1024}, jobs.NewStore(time.Hour), jobs.NewWorker(jobs.NewStore(time.Hour), fakeRunner{}), func() bool { return false })
	req := httptest.NewRequest(http.MethodPost, "/v1/transcribe", nil)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestCreateRequiresFile(t *testing.T) {
	h := NewHandler(config.Config{MaxUploadBytes: 1024}, jobs.NewStore(time.Hour), jobs.NewWorker(jobs.NewStore(time.Hour), fakeRunner{}), func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/v1/transcribe", nil)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestGetMissingJob(t *testing.T) {
	h := NewHandler(config.Config{}, jobs.NewStore(time.Hour), jobs.NewWorker(jobs.NewStore(time.Hour), fakeRunner{}), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, getRequest("missing"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestCardEndpoint(t *testing.T) {
	store := jobs.NewStore(time.Hour)
	h := NewHandler(config.Config{}, store, jobs.NewWorker(store, fakeRunner{}), nil)
	store.Create("j1", "a.wav", []byte("x"))
	store.Finish("j1", jobs.Result{Text: "a"})
	store.StartCard("j1")
	store.FinishCard("j1", &card.Card{Template: "vet_appointment_v1", Text: "Питомец: —"})

	rec := httptest.NewRecorder()
	h.Card(rec, getRequest("j1"))
	var got struct {
		JobID  string     `json:"job_id"`
		Status string     `json:"status"`
		Card   *card.Card `json:"card"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if got.JobID != "j1" || got.Status != "done" || got.Card == nil || got.Card.Text != "Питомец: —" {
		t.Fatalf("card response %+v", got)
	}

	job := getJob(t, h, "j1")
	if job["card_status"] != "done" {
		t.Fatalf("job card_status %v", job["card_status"])
	}

	rec = httptest.NewRecorder()
	h.Card(rec, getRequest("missing"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing job status %d", rec.Code)
	}
}

func getJob(t *testing.T, h *Handler, id string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Get(rec, getRequest(id))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status %d body %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func getRequest(id string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/transcribe/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("jobID", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

package transcribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nikenesx/aura-server/internal/config"
)

func TestHandlerCreate(t *testing.T) {
	t.Parallel()

	asr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/transcribe":
			if r.Header.Get("X-Filename") == "" {
				t.Error("missing X-Filename")
			}
			if r.ContentLength == 0 {
				t.Error("empty body")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Result{
				Text:    "привет",
				Phrases: []Phrase{{Text: "привет", Start: 1.79, End: 2.04}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(asr.Close)

	h := NewHandler(config.Config{MaxUploadBytes: 1024 * 1024}, NewClient(asr.URL, time.Second))

	var body strings.Builder
	writer := newMultipart(&body, "file", "call.wav", "RIFF")
	req := httptest.NewRequest(http.MethodPost, "/v1/transcribe", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer)
	rec := httptest.NewRecorder()

	h.Create(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	var got Result
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Text != "привет" {
		t.Fatalf("text = %q", got.Text)
	}
}

func TestHandlerUnavailable(t *testing.T) {
	t.Parallel()

	h := NewHandler(config.Config{MaxUploadBytes: 1024}, NewClient("http://127.0.0.1:1", time.Millisecond))
	req := httptest.NewRequest(http.MethodPost, "/v1/transcribe", nil)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func newMultipart(body *strings.Builder, field, filename, content string) string {
	const boundary = "testhandlerboundary"
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"" + field + "\"; filename=\"" + filename + "\"\r\n")
	body.WriteString("Content-Type: audio/wav\r\n\r\n")
	body.WriteString(content)
	body.WriteString("\r\n--" + boundary + "--\r\n")
	return "multipart/form-data; boundary=" + boundary
}

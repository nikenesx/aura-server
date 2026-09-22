package transcribe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nikenesx/aura-server/internal/jobs"
)

func TestHealthyRemembersRecentSuccess(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		time.Sleep(4 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, time.Minute)
	if !c.Healthy(context.Background()) {
		t.Fatal("first probe should succeed")
	}
	if !c.Healthy(context.Background()) {
		t.Fatal("busy ASR should still look healthy after a recent success")
	}
}

func TestTranscribeBusyIsErrBusy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"detail":"asr is busy"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, time.Minute).Transcribe(context.Background(), "a.wav", []byte("x"))
	if !errors.Is(err, jobs.ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
}

func TestHealthyFalseWhenNeverReached(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", time.Second)
	if c.Healthy(context.Background()) {
		t.Fatal("expected unhealthy")
	}
}

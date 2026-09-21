package transcribe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestHealthyFalseWhenNeverReached(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", time.Second)
	if c.Healthy(context.Background()) {
		t.Fatal("expected unhealthy")
	}
}

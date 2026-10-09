package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJSONSendsSchemaAndReturnsContent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "t-lite", time.Minute)
	raw, err := c.JSON(context.Background(), []Message{{Role: "user", Content: "hi"}}, "card", map[string]any{"type": "object"}, Sampling{Temperature: 0.2, MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("content %s", raw)
	}
	format := got["response_format"].(map[string]any)
	if got["model"] != "t-lite" || format["type"] != "json_schema" || format["json_schema"].(map[string]any)["strict"] != true {
		t.Fatalf("request %v", got)
	}
}

func TestJSONErrors(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		unavailable bool
	}{
		{"loading", http.StatusServiceUnavailable, `{"message":"loading"}`, true},
		{"overloaded", http.StatusTooManyRequests, `{}`, true},
		{"too long", http.StatusBadRequest, `{"error":{"message":"maximum context length"}}`, false},
		{"cut off", http.StatusOK, `{"choices":[{"message":{"content":"{\"a\":"},"finish_reason":"length"}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			_, err := NewClient(srv.URL, "m", time.Minute).JSON(context.Background(), nil, "card", nil, Sampling{})
			if err == nil || errors.Is(err, ErrUnavailable) != tc.unavailable {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestJSONUnreachableIsUnavailable(t *testing.T) {
	_, err := NewClient("http://127.0.0.1:1", "m", time.Second).JSON(context.Background(), nil, "card", nil, Sampling{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

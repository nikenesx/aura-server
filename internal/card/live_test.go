package card

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/nikenesx/aura-server/internal/llm"
)

// TestLiveCard runs a real transcript through a running LLM. It is skipped
// unless AURA_LLM_URL is set:
//
//	AURA_LLM_URL=http://127.0.0.1:8000 AURA_TRANSCRIPT=result.json \
//	    go test ./internal/card -run Live -v
//
// AURA_TRANSCRIPT is what bench.py --result writes.
func TestLiveCard(t *testing.T) {
	url, path := os.Getenv("AURA_LLM_URL"), os.Getenv("AURA_TRANSCRIPT")
	if url == "" || path == "" {
		t.Skip("set AURA_LLM_URL and AURA_TRANSCRIPT to run against a real model")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var transcript struct {
		Phrases []struct {
			Speaker string `json:"speaker"`
			Text    string `json:"text"`
		} `json:"phrases"`
	}
	if err := json.Unmarshal(raw, &transcript); err != nil {
		t.Fatal(err)
	}
	lines := make([]Line, len(transcript.Phrases))
	for i, p := range transcript.Phrases {
		lines[i] = Line{Speaker: p.Speaker, Text: p.Text}
	}

	model := llm.NewClient(url, envOr("AURA_LLM_MODEL", "t-lite"), 10*time.Minute)
	started := time.Now()
	c, err := NewGenerator(model, VetAppointment).Make(context.Background(), lines)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d phrases in %.1fs, roles %v\n\n%s", len(lines), time.Since(started).Seconds(), c.Roles, c.Text)

	filled := 0
	for _, s := range c.Sections {
		for _, f := range s.Fields {
			if f.Value != nil || len(f.Items) > 0 {
				filled++
			}
		}
	}
	if filled == 0 {
		t.Fatal("every field came back empty")
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

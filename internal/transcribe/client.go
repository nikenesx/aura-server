package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/nikenesx/aura-server/internal/jobs"
)

const healthyGrace = 30 * time.Minute

type Client struct {
	baseURL    string
	httpClient *http.Client

	mu     sync.Mutex
	lastOK time.Time
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Healthy(ctx context.Context) bool {
	if c.probe(ctx) {
		c.mu.Lock()
		c.lastOK = time.Now()
		c.mu.Unlock()
		return true
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// One uvicorn worker cannot answer /health while it is transcribing.
	// A recent success means ASR is up and likely just busy.
	return !c.lastOK.IsZero() && time.Since(c.lastOK) < healthyGrace
}

func (c *Client) probe(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	client := *c.httpClient
	client.Timeout = 3 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) Transcribe(ctx context.Context, filename string, audio []byte) (jobs.Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/transcribe", bytes.NewReader(audio))
	if err != nil {
		return jobs.Result{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Filename", filename)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return jobs.Result{}, fmt.Errorf("call asr: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return jobs.Result{}, fmt.Errorf("read asr response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return jobs.Result{}, fmt.Errorf("asr status %d: %s", resp.StatusCode, asrDetail(payload))
	}

	var result jobs.Result
	if err := json.Unmarshal(payload, &result); err != nil {
		return jobs.Result{}, fmt.Errorf("decode asr response: %w", err)
	}
	if result.Phrases == nil {
		result.Phrases = []jobs.Phrase{}
	}
	return result, nil
}

func asrDetail(body []byte) string {
	var parsed struct {
		Detail any `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Detail != nil {
		return fmt.Sprint(parsed.Detail)
	}
	return string(body)
}

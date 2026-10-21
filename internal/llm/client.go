// Package llm talks to an OpenAI-compatible chat completions server (vLLM).
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnavailable means the server could not take the request right now:
// it is down, still loading the model, or overloaded. Worth retrying.
var ErrUnavailable = errors.New("llm unavailable")

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Sampling holds generation settings. Extraction wants low temperature and
// no presence penalty: repeating the transcript's words is the point.
type Sampling struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	TopK        int     `json:"top_k"`
	MaxTokens   int     `json:"max_tokens"`
}

type Client struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

func NewClient(baseURL, model string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Healthy(ctx context.Context) bool {
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

// JSON asks for a reply that matches schema and returns the raw JSON text.
func (c *Client) JSON(ctx context.Context, messages []Message, name string, schema any, s Sampling) (json.RawMessage, error) {
	return c.JSONStream(ctx, messages, name, schema, s, nil)
}

// JSONStream is JSON that reports how many tokens the reply has so far.
// With a nil onToken it makes a plain, non-streaming request.
func (c *Client) JSONStream(ctx context.Context, messages []Message, name string, schema any, s Sampling, onToken func(tokens int)) (json.RawMessage, error) {
	request := map[string]any{
		"model":       c.model,
		"messages":    messages,
		"temperature": s.Temperature,
		"top_p":       s.TopP,
		"top_k":       s.TopK,
		"max_tokens":  s.MaxTokens,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   name,
				"schema": schema,
				"strict": true,
			},
		},
	}
	if onToken != nil {
		request["stream"] = true
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: status %d: %s", ErrUnavailable, resp.StatusCode, errorDetail(payload))
		}
		return nil, fmt.Errorf("llm status %d: %s", resp.StatusCode, errorDetail(payload))
	}

	var content, finish string
	if onToken != nil {
		content, finish, err = readStream(resp.Body, onToken)
	} else {
		content, finish, err = readReply(resp.Body)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if finish == "length" {
		return nil, fmt.Errorf("llm reply cut at %d tokens", s.MaxTokens)
	}
	return json.RawMessage(content), nil
}

func readReply(body io.Reader) (content, finish string, err error) {
	payload, err := io.ReadAll(io.LimitReader(body, 4<<20))
	if err != nil {
		return "", "", fmt.Errorf("read llm response: %w", err)
	}
	var parsed struct {
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", "", fmt.Errorf("decode llm response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", "", errors.New("llm returned no choices")
	}
	return parsed.Choices[0].Message.Content, parsed.Choices[0].FinishReason, nil
}

// readStream reads server-sent events. vLLM sends about one token per event.
func readStream(body io.Reader, onToken func(int)) (content, finish string, err error) {
	var b strings.Builder
	tokens := 0
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return "", "", fmt.Errorf("decode llm stream: %w", err)
		}
		if event.Error != nil {
			return "", "", fmt.Errorf("llm stream: %s", event.Error.Message)
		}
		for _, ch := range event.Choices {
			if ch.Delta.Content != "" {
				b.WriteString(ch.Delta.Content)
				tokens++
				onToken(tokens)
			}
			if ch.FinishReason != nil {
				finish = *ch.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("read llm stream: %w", err)
	}
	return b.String(), finish, nil
}

func errorDetail(body []byte) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Error.Message != "" {
			return parsed.Error.Message
		}
		if parsed.Message != "" {
			return parsed.Message
		}
	}
	return string(body)
}

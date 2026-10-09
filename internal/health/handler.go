package health

import (
	"context"
	"encoding/json"
	"net/http"
)

type Handler struct {
	ASROK func(context.Context) bool
	// LLMOK is nil when no LLM is configured.
	LLMOK func(context.Context) bool
}

type response struct {
	Status string `json:"status"`
	ASR    string `json:"asr"`
	LLM    string `json:"llm"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response{
		Status: "ok",
		ASR:    probe(r.Context(), h.ASROK, "unavailable"),
		LLM:    probe(r.Context(), h.LLMOK, "disabled"),
	})
}

func probe(ctx context.Context, ok func(context.Context) bool, missing string) string {
	switch {
	case ok == nil:
		return missing
	case ok(ctx):
		return "ok"
	default:
		return "unavailable"
	}
}

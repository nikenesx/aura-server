package health

import (
	"context"
	"encoding/json"
	"net/http"
)

type Handler struct {
	ASROK func(context.Context) bool
}

type response struct {
	Status string `json:"status"`
	ASR    string `json:"asr"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	asr := "unavailable"
	if h.ASROK != nil && h.ASROK(r.Context()) {
		asr = "ok"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response{Status: "ok", ASR: asr})
}

package transcribe

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/nikenesx/aura-server/internal/config"
)

type Handler struct {
	cfg    config.Config
	client *Client
}

func NewHandler(cfg config.Config, client *Client) *Handler {
	return &Handler{cfg: cfg, client: client}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.client.Healthy(r.Context()) {
		writeError(w, http.StatusServiceUnavailable, "transcription service is unavailable")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxUploadBytes)
	if err := r.ParseMultipartForm(h.cfg.MaxUploadBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "audio is too large or invalid multipart body")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "multipart field \"file\" is required")
		return
	}
	defer file.Close()

	audio, err := io.ReadAll(io.LimitReader(file, h.cfg.MaxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read audio")
		return
	}
	if int64(len(audio)) > h.cfg.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "audio is too large")
		return
	}
	if len(audio) == 0 {
		writeError(w, http.StatusBadRequest, "audio file is empty")
		return
	}

	name := filepath.Base(header.Filename)
	if name == "" || name == "." {
		name = "audio"
	}

	result, err := h.client.Transcribe(r.Context(), name, audio)
	if err != nil {
		if strings.Contains(err.Error(), "asr status 429") {
			writeError(w, http.StatusTooManyRequests, "transcription service is busy")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

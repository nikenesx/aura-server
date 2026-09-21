package transcribe

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nikenesx/aura-server/internal/config"
	"github.com/nikenesx/aura-server/internal/jobs"
)

type Handler struct {
	cfg     config.Config
	store   *jobs.Store
	worker  *jobs.Worker
	healthy func() bool
}

func NewHandler(cfg config.Config, store *jobs.Store, worker *jobs.Worker, healthy func() bool) *Handler {
	return &Handler{cfg: cfg, store: store, worker: worker, healthy: healthy}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if h.healthy != nil && !h.healthy() {
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

	id := uuid.NewString()
	h.store.Create(id, name, audio)
	h.worker.Enqueue(id)
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "jobID")
	if id == "" {
		writeError(w, http.StatusBadRequest, "job_id is required")
		return
	}
	job, ok := h.store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, jobView(job))
}

type jobViewResponse struct {
	JobID    string       `json:"job_id"`
	Status   jobs.Status  `json:"status"`
	Filename string       `json:"filename,omitempty"`
	Bytes    int          `json:"bytes,omitempty"`
	Error    string       `json:"error,omitempty"`
	Result   *jobs.Result `json:"result,omitempty"`
}

func jobView(job *jobs.Job) jobViewResponse {
	return jobViewResponse{
		JobID:    job.ID,
		Status:   job.Status,
		Filename: job.Name,
		Bytes:    job.Bytes,
		Error:    job.Error,
		Result:   job.Result,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

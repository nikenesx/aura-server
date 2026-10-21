// Package appointments serves a doctor's recordings, transcripts and cards.
package appointments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nikenesx/aura-server/internal/audio"
	"github.com/nikenesx/aura-server/internal/auth"
	"github.com/nikenesx/aura-server/internal/card"
	"github.com/nikenesx/aura-server/internal/httpx"
	"github.com/nikenesx/aura-server/internal/jobs"
	"github.com/nikenesx/aura-server/internal/store"
)

const (
	defaultPage = 30
	maxPage     = 100
)

type Store interface {
	CreateAppointment(ctx context.Context, a store.Appointment) error
	Appointment(ctx context.Context, id, userID string) (store.Appointment, error)
	ListAppointments(ctx context.Context, userID string, before time.Time, limit int) ([]store.Appointment, error)
	SaveCardEdit(ctx context.Context, id, userID string, card json.RawMessage) error
	RestartCard(ctx context.Context, id, userID string, progress int) error
}

type Recordings interface {
	Put(key string, r io.Reader, limit int64) (int64, error)
	Open(key string) (audio.File, error)
	Delete(key string) error
}

type Worker interface {
	Notify()
	NotifyCards()
	CardsEnabled() bool
}

type Handler struct {
	db     Store
	audio  Recordings
	worker Worker
	// MaxUpload caps one recording; UploadTimeout is how long a single upload
	// or download may take, far past the server's default read deadline.
	MaxUpload     int64
	UploadTimeout time.Duration
}

func NewHandler(db Store, recordings Recordings, worker Worker) *Handler {
	return &Handler{
		db:            db,
		audio:         recordings,
		worker:        worker,
		MaxUpload:     512 << 20,
		UploadTimeout: 30 * time.Minute,
	}
}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/v1/appointments", h.Create)
	r.Get("/v1/appointments", h.List)
	r.Get("/v1/appointments/{id}", h.Get)
	r.Put("/v1/appointments/{id}/card", h.SaveCard)
	r.Post("/v1/appointments/{id}/card/regenerate", h.Regenerate)
	r.Get("/v1/appointments/{id}/audio", h.Audio)
}

// Create takes a multipart "file" field and streams it to disk: a long
// recording never sits in memory.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	// An hour of audio on a clinic's uplink takes longer than the server-wide read deadline.
	extendDeadline(http.NewResponseController(w).SetReadDeadline, h.UploadTimeout)
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxUpload+1<<20)

	mr, err := r.MultipartReader()
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "multipart body with a \"file\" field is required")
		return
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			httpx.WriteError(w, http.StatusBadRequest, "multipart field \"file\" is required")
			return
		}
		if err != nil {
			h.uploadError(w, err)
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		h.save(w, r, user, part)
		return
	}
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request, user store.User, part *multipart.Part) {
	filename := filepath.Base(part.FileName())
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = "audio"
	}
	id := uuid.NewString()
	key := id + safeExt(filename)

	n, err := h.audio.Put(key, part, h.MaxUpload)
	if err != nil {
		_ = h.audio.Delete(key)
		h.uploadError(w, err)
		return
	}
	if n == 0 {
		_ = h.audio.Delete(key)
		httpx.WriteError(w, http.StatusBadRequest, "audio file is empty")
		return
	}

	err = h.db.CreateAppointment(r.Context(), store.Appointment{
		ID:         id,
		UserID:     user.ID,
		CreatedAt:  time.Now(),
		Filename:   filename,
		AudioKey:   key,
		AudioBytes: n,
	})
	if err != nil {
		_ = h.audio.Delete(key)
		slog.Error("create appointment", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.worker.Notify()
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (h *Handler) uploadError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.Is(err, audio.ErrTooLarge) || errors.As(err, &tooBig) {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "audio is too large")
		return
	}
	slog.Warn("upload failed", "error", err)
	httpx.WriteError(w, http.StatusBadRequest, "upload failed: "+err.Error())
}

// List returns the doctor's appointments newest first. Pass the last item's
// created_at as ?before= for the next page.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	limit := defaultPage
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			httpx.WriteError(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		limit = min(n, maxPage)
	}
	var before time.Time
	if raw := r.URL.Query().Get("before"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "before must be an RFC 3339 time")
			return
		}
		before = t
	}

	list, err := h.db.ListAppointments(r.Context(), user.ID, before, limit)
	if err != nil {
		slog.Error("list appointments", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	items := make([]summaryView, len(list))
	for i, a := range list {
		items[i] = summary(a)
	}
	resp := map[string]any{"items": items}
	if len(list) == limit {
		resp["next_before"] = list[len(list)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	a, ok := h.find(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail(a))
}

// SaveCard stores the doctor's edits. The model's version stays in the database.
func (h *Handler) SaveCard(w http.ResponseWriter, r *http.Request) {
	a, ok := h.find(w, r)
	if !ok {
		return
	}
	if len(a.Card) == 0 {
		httpx.WriteError(w, http.StatusConflict, "the card is not ready yet")
		return
	}
	var edit card.Edit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&edit); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "body must be a card")
		return
	}
	base, err := card.Decode(a.Card)
	if err != nil {
		slog.Error("decode stored card", "id", a.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	edited, ok := card.ApplyEdit(base, edit, phraseCount(a.Transcript))
	if !ok {
		httpx.WriteError(w, http.StatusConflict, "unknown card template")
		return
	}
	raw, err := json.Marshal(edited)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if err := h.db.SaveCardEdit(r.Context(), a.ID, user.ID, raw); err != nil {
		slog.Error("save card", "id", a.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, json.RawMessage(raw))
}

// Regenerate makes the card again, for when the LLM failed or the doctor wants
// a fresh draft. It replaces the doctor's edits.
func (h *Handler) Regenerate(w http.ResponseWriter, r *http.Request) {
	a, ok := h.find(w, r)
	if !ok {
		return
	}
	if !h.worker.CardsEnabled() {
		httpx.WriteError(w, http.StatusConflict, jobs.CardsDisabled)
		return
	}
	user, _ := auth.UserFrom(r.Context())
	err := h.db.RestartCard(r.Context(), a.ID, user.ID, jobs.ProgressWritingCard)
	if errors.Is(err, store.ErrNotFound) {
		httpx.WriteError(w, http.StatusConflict, "only a finished appointment with a transcript can get a new card")
		return
	}
	if err != nil {
		slog.Error("regenerate card", "id", a.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.worker.NotifyCards()
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"id": a.ID})
}

// Audio serves the recording with Range support, so the player can seek.
func (h *Handler) Audio(w http.ResponseWriter, r *http.Request) {
	a, ok := h.find(w, r)
	if !ok {
		return
	}
	f, err := h.audio.Open(a.AudioKey)
	if errors.Is(err, audio.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "audio not found")
		return
	}
	if err != nil {
		slog.Error("open audio", "id", a.ID, "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Sending an hour of audio takes longer than the server-wide write deadline.
	extendDeadline(http.NewResponseController(w).SetWriteDeadline, h.UploadTimeout)
	if ct := contentType(a.AudioKey); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeContent(w, r, a.Filename, info.ModTime(), f)
}

func (h *Handler) find(w http.ResponseWriter, r *http.Request) (store.Appointment, bool) {
	user, _ := auth.UserFrom(r.Context())
	a, err := h.db.Appointment(r.Context(), chi.URLParam(r, "id"), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "appointment not found")
		return store.Appointment{}, false
	}
	if err != nil {
		slog.Error("get appointment", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return store.Appointment{}, false
	}
	return a, true
}

// extendDeadline lifts the server-wide deadline for one long request. It only
// works if every middleware's ResponseWriter wrapper implements Unwrap, so a
// failure is logged rather than left to show up as cut-off uploads.
func extendDeadline(set func(time.Time) error, d time.Duration) {
	if err := set(time.Now().Add(d)); err != nil {
		slog.Warn("cannot extend request deadline; long uploads and downloads may be cut off", "error", err)
	}
}

var extPattern = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// safeExt keeps the extension for the player's sake, but only a plain one.
func safeExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if extPattern.MatchString(ext) {
		return ext
	}
	return ".audio"
}

func contentType(key string) string {
	switch ext := filepath.Ext(key); ext {
	case ".m4a", ".mp4", ".aac":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	default:
		return mime.TypeByExtension(ext)
	}
}

func phraseCount(transcript json.RawMessage) int {
	var t struct {
		Phrases []json.RawMessage `json:"phrases"`
	}
	_ = json.Unmarshal(transcript, &t)
	return len(t.Phrases)
}

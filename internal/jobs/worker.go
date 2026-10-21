// Package jobs runs appointments through the pipeline: transcript, speaker
// roles, then the card.
//
// The database is the queue. Each stage takes the oldest appointment in its
// statuses, so nothing is lost on restart and there is no queue to fill up;
// the wake channels only cut the wait when new work arrives.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/nikenesx/aura-server/internal/audio"
	"github.com/nikenesx/aura-server/internal/card"
	"github.com/nikenesx/aura-server/internal/store"
)

// ErrBusy means the runner cannot take the job right now: it is busy with
// another one or unreachable. The job is retried instead of failed.
var ErrBusy = errors.New("runner is busy")

// CardsDisabled is the card error when no LLM is configured.
const CardsDisabled = "appointment cards are disabled: LLM_URL is not set"

// Progress milestones out of 100, weighted by how long each stage takes:
// transcription ~45%, roles a few seconds, the card the rest.
const (
	ProgressTranscribing = 2
	ProgressTranscribed  = 45
	ProgressWritingCard  = 50
	progressCardDone     = 99
)

type Appointments interface {
	Next(ctx context.Context, statuses ...store.Status) (store.Appointment, error)
	ResetInterrupted(ctx context.Context) (int64, error)
	SetStatus(ctx context.Context, id string, status store.Status, progress int) error
	SetProgress(ctx context.Context, id string, progress int) error
	SaveTranscript(ctx context.Context, id string, transcript, waveform json.RawMessage, durationSec float64) error
	SaveRoles(ctx context.Context, id string, roles json.RawMessage) error
	FinishCard(ctx context.Context, id string, card json.RawMessage) error
	FailCard(ctx context.Context, id, message string) error
	Fail(ctx context.Context, id, message string) error
}

type Recordings interface {
	Open(key string) (audio.File, error)
}

type Runner interface {
	Transcribe(ctx context.Context, filename string, body io.Reader) (Result, error)
}

// ProgressReporter is a Runner that can say how far the recording in flight
// has got, 0..1.
type ProgressReporter interface {
	Progress(ctx context.Context) (fraction float64, active bool, err error)
}

// CardMaker labels the speakers and builds the card from a finished transcript.
type CardMaker interface {
	Roles(ctx context.Context, lines []card.Line) (map[string]string, error)
	Make(ctx context.Context, lines []card.Line, roles map[string]string, onProgress func(float64)) (*card.Card, error)
}

type Worker struct {
	db     Appointments
	audio  Recordings
	runner Runner
	cards  CardMaker

	wakeTranscribe chan struct{}
	wakeCards      chan struct{}

	busyMinDelay  time.Duration
	busyMaxDelay  time.Duration
	busyGiveUp    time.Duration
	idlePoll      time.Duration
	errorPause    time.Duration
	progressEvery time.Duration
}

func NewWorker(db Appointments, recordings Recordings, runner Runner) *Worker {
	return &Worker{
		db:             db,
		audio:          recordings,
		runner:         runner,
		wakeTranscribe: make(chan struct{}, 1),
		wakeCards:      make(chan struct{}, 1),
		busyMinDelay:   2 * time.Second,
		busyMaxDelay:   30 * time.Second,
		busyGiveUp:     30 * time.Minute,
		// A safety net in case a wake-up is missed; new work normally starts at once.
		idlePoll:      30 * time.Second,
		errorPause:    5 * time.Second,
		progressEvery: time.Second,
	}
}

// EnableCards labels speakers and makes a card after every transcript. Call
// it before Start; without it the card error says cards are disabled.
func (w *Worker) EnableCards(maker CardMaker) {
	w.cards = maker
}

func (w *Worker) CardsEnabled() bool {
	return w.cards != nil
}

// Start resumes what a restart interrupted and runs both stages until ctx ends.
func (w *Worker) Start(ctx context.Context) error {
	n, err := w.db.ResetInterrupted(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("resuming interrupted transcriptions", "count", n)
	}
	go w.run(ctx, w.wakeTranscribe, w.transcribe, store.StatusQueued)
	if w.cards != nil {
		go w.run(ctx, w.wakeCards, w.makeCard, store.StatusRoles, store.StatusWritingCard)
	}
	return nil
}

// Notify says a new recording is waiting. It never blocks.
func (w *Worker) Notify() { wake(w.wakeTranscribe) }

// NotifyCards says a card is waiting to be made. It never blocks.
func (w *Worker) NotifyCards() { wake(w.wakeCards) }

func wake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (w *Worker) run(ctx context.Context, wakeCh <-chan struct{}, handle func(context.Context, store.Appointment) error, statuses ...store.Status) {
	for ctx.Err() == nil {
		a, err := w.db.Next(ctx, statuses...)
		if err == nil {
			err = handle(ctx, a)
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, store.ErrNotFound):
			select {
			case <-ctx.Done():
			case <-wakeCh:
			case <-time.After(w.idlePoll):
			}
		default:
			// The database itself is failing; do not spin on the same row.
			if ctx.Err() == nil {
				slog.Error("worker", "statuses", statuses, "error", err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(w.errorPause):
			}
		}
	}
}

func (w *Worker) transcribe(ctx context.Context, a store.Appointment) error {
	if err := w.db.SetStatus(ctx, a.ID, store.StatusTranscribing, ProgressTranscribing); err != nil {
		return err
	}
	slog.Info("transcribe started", "id", a.ID, "filename", a.Filename, "bytes", a.AudioBytes)

	result, err := w.runASR(ctx, a)
	if err != nil {
		if ctx.Err() != nil {
			// Shutting down: the status stays "transcribing" and Start resets it.
			return ctx.Err()
		}
		slog.Error("transcribe failed", "id", a.ID, "error", err)
		return w.db.Fail(ctx, a.ID, err.Error())
	}

	if result.Phrases == nil {
		result.Phrases = []Phrase{}
	}
	transcript, err := json.Marshal(Result{Text: result.Text, Phrases: result.Phrases})
	if err != nil {
		return err
	}
	var waveform json.RawMessage
	if len(result.Waveform) > 0 {
		if waveform, err = json.Marshal(result.Waveform); err != nil {
			return err
		}
	}
	if err := w.db.SaveTranscript(ctx, a.ID, transcript, waveform, result.Duration); err != nil {
		return err
	}
	slog.Info("transcribe done", "id", a.ID, "phrases", len(result.Phrases))

	if w.cards == nil {
		return w.db.FailCard(ctx, a.ID, CardsDisabled)
	}
	if err := w.db.SetStatus(ctx, a.ID, store.StatusRoles, ProgressTranscribed); err != nil {
		return err
	}
	w.NotifyCards()
	return nil
}

// runASR sends the recording, retrying while the runner is busy with backoff
// up to busyMaxDelay, and gives up after busyGiveUp. The file is reopened for
// each attempt because a failed request may have read part of it.
func (w *Worker) runASR(ctx context.Context, a store.Appointment) (Result, error) {
	pollCtx, stopPoll := context.WithCancel(ctx)
	var wg sync.WaitGroup
	if p, ok := w.runner.(ProgressReporter); ok {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.pollASR(pollCtx, a.ID, p)
		}()
	}
	defer func() {
		stopPoll()
		wg.Wait()
	}()

	deadline := time.Now().Add(w.busyGiveUp)
	delay := w.busyMinDelay
	for {
		f, err := w.audio.Open(a.AudioKey)
		if err != nil {
			return Result{}, err
		}
		result, err := w.runner.Transcribe(ctx, a.Filename, f)
		_ = f.Close()
		if !errors.Is(err, ErrBusy) || time.Now().Add(delay).After(deadline) {
			return result, err
		}
		slog.Warn("transcribe runner busy, retrying", "id", a.ID, "delay", delay, "error", err)
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, w.busyMaxDelay)
	}
}

// pollASR copies the ASR service's progress into the appointment until ctx
// ends. Failures are ignored: the bar just waits for the next stage.
func (w *Worker) pollASR(ctx context.Context, id string, p ProgressReporter) {
	ticker := time.NewTicker(w.progressEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		fraction, active, err := p.Progress(ctx)
		if err != nil || !active {
			continue
		}
		_ = w.db.SetProgress(ctx, id, scale(fraction, ProgressTranscribing, ProgressTranscribed))
	}
}

func (w *Worker) makeCard(ctx context.Context, a store.Appointment) error {
	var transcript Result
	if err := json.Unmarshal(a.Transcript, &transcript); err != nil {
		return w.db.FailCard(ctx, a.ID, "transcript is unreadable: "+err.Error())
	}
	lines := make([]card.Line, len(transcript.Phrases))
	for i, p := range transcript.Phrases {
		lines[i] = card.Line{Speaker: p.Speaker, Text: p.Text}
	}

	var roles map[string]string
	if len(a.Roles) > 0 {
		_ = json.Unmarshal(a.Roles, &roles)
	}
	if a.Status == store.StatusRoles {
		roles = w.labelSpeakers(ctx, a.ID, lines)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.db.SetStatus(ctx, a.ID, store.StatusWritingCard, ProgressWritingCard); err != nil {
			return err
		}
	}

	started := time.Now()
	c, err := w.cards.Make(ctx, lines, roles, w.cardProgress(ctx, a.ID))
	if err != nil {
		if ctx.Err() != nil {
			// Shutting down: the status stays and the card is redone.
			return ctx.Err()
		}
		slog.Error("card failed", "id", a.ID, "error", err)
		return w.db.FailCard(ctx, a.ID, err.Error())
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := w.db.FinishCard(ctx, a.ID, raw); err != nil {
		return err
	}
	slog.Info("card done", "id", a.ID, "phrases", len(lines), "seconds", time.Since(started).Seconds())
	return nil
}

// labelSpeakers stores who is the doctor and who the owner. On failure the
// card goes ahead and decides the roles itself.
func (w *Worker) labelSpeakers(ctx context.Context, id string, lines []card.Line) map[string]string {
	started := time.Now()
	roles, err := w.cards.Roles(ctx, lines)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("roles failed; the card will decide them", "id", id, "error", err)
		}
		return nil
	}
	raw, err := json.Marshal(roles)
	if err == nil {
		err = w.db.SaveRoles(ctx, id, raw)
	}
	if err != nil {
		slog.Warn("save roles", "id", id, "error", err)
	}
	slog.Info("roles done", "id", id, "roles", roles, "seconds", time.Since(started).Seconds())
	return roles
}

// cardProgress turns the card's 0..1 into the appointment's bar, writing to
// the database only when the bar moves a whole percent.
func (w *Worker) cardProgress(ctx context.Context, id string) func(float64) {
	last := ProgressWritingCard
	return func(f float64) {
		p := scale(f, ProgressWritingCard, progressCardDone)
		if p > last {
			last = p
			_ = w.db.SetProgress(ctx, id, p)
		}
	}
}

func scale(f float64, lo, hi int) int {
	f = min(max(f, 0), 1)
	return lo + int(f*float64(hi-lo))
}

package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nikenesx/aura-server/internal/card"
)

// ErrBusy means the runner is occupied with another job. The job is retried
// instead of failed.
var ErrBusy = errors.New("runner is busy")

type Runner interface {
	Transcribe(ctx context.Context, filename string, audio []byte) (Result, error)
}

// CardMaker builds the appointment card from a finished transcript.
type CardMaker interface {
	Make(ctx context.Context, lines []card.Line) (*card.Card, error)
}

type Worker struct {
	store  *Store
	runner Runner
	queue  chan string
	cards  CardMaker
	// Cards have their own queue, so the next recording is transcribed while
	// the LLM writes the previous card.
	cardQueue chan string

	busyMinDelay time.Duration
	busyMaxDelay time.Duration
	busyGiveUp   time.Duration
}

func NewWorker(store *Store, runner Runner) *Worker {
	return &Worker{
		store:        store,
		runner:       runner,
		queue:        make(chan string, 64),
		busyMinDelay: 2 * time.Second,
		busyMaxDelay: 30 * time.Second,
		busyGiveUp:   30 * time.Minute,
		cardQueue:    make(chan string, 64),
	}
}

// EnableCards makes a card after every transcript. Call it before Start;
// without it cards are "disabled".
func (w *Worker) EnableCards(maker CardMaker) {
	w.cards = maker
}

func (w *Worker) Start(ctx context.Context) {
	go w.sweepLoop(ctx)
	go w.loop(ctx)
	if w.cards != nil {
		go w.cardLoop(ctx)
	}
}

func (w *Worker) Enqueue(id string) {
	w.queue <- id
}

func (w *Worker) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-w.queue:
			w.process(ctx, id)
		}
	}
}

func (w *Worker) process(ctx context.Context, id string) {
	name, audio, ok := w.store.TakeAudio(id)
	if !ok {
		w.store.Fail(id, "job audio is missing")
		return
	}

	slog.Info("transcribe job started", "job_id", id, "filename", name, "bytes", len(audio))
	result, err := w.transcribe(ctx, id, name, audio)
	if err != nil {
		slog.Error("transcribe job failed", "job_id", id, "error", err)
		w.store.Fail(id, err.Error())
		w.store.FailCard(id, "transcription failed")
		return
	}
	if result.Phrases == nil {
		result.Phrases = []Phrase{}
	}
	w.store.Finish(id, result)
	slog.Info("transcribe job done", "job_id", id, "phrases", len(result.Phrases))

	if w.cards == nil {
		w.store.DisableCard(id)
		return
	}
	w.cardQueue <- id
}

func (w *Worker) cardLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-w.cardQueue:
			w.makeCard(ctx, id)
		}
	}
}

func (w *Worker) makeCard(ctx context.Context, id string) {
	result, ok := w.store.StartCard(id)
	if !ok {
		return
	}
	lines := make([]card.Line, len(result.Phrases))
	for i, p := range result.Phrases {
		lines[i] = card.Line{Speaker: p.Speaker, Text: p.Text}
	}

	started := time.Now()
	c, err := w.cards.Make(ctx, lines)
	if err != nil {
		slog.Error("card failed", "job_id", id, "error", err)
		w.store.FailCard(id, err.Error())
		return
	}
	w.store.FinishCard(id, c)
	slog.Info("card done", "job_id", id, "phrases", len(lines), "seconds", time.Since(started).Seconds())
}

// transcribe retries while the runner is busy, backing off up to busyMaxDelay,
// and gives up after busyGiveUp.
func (w *Worker) transcribe(ctx context.Context, id, name string, audio []byte) (Result, error) {
	deadline := time.Now().Add(w.busyGiveUp)
	delay := w.busyMinDelay
	for {
		result, err := w.runner.Transcribe(ctx, name, audio)
		if !errors.Is(err, ErrBusy) || time.Now().Add(delay).After(deadline) {
			return result, err
		}
		slog.Warn("transcribe runner busy, retrying", "job_id", id, "delay", delay)
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, w.busyMaxDelay)
	}
}

func (w *Worker) sweepLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			w.store.Sweep(now)
		}
	}
}

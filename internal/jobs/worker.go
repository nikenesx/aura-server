package jobs

import (
	"context"
	"log/slog"
	"time"
)

type Runner interface {
	Transcribe(ctx context.Context, filename string, audio []byte) (Result, error)
}

type Worker struct {
	store  *Store
	runner Runner
	queue  chan string
}

func NewWorker(store *Store, runner Runner) *Worker {
	return &Worker{
		store:  store,
		runner: runner,
		queue:  make(chan string, 64),
	}
}

func (w *Worker) Start(ctx context.Context) {
	go w.sweepLoop(ctx)
	go w.loop(ctx)
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
	result, err := w.runner.Transcribe(ctx, name, audio)
	if err != nil {
		slog.Error("transcribe job failed", "job_id", id, "error", err)
		w.store.Fail(id, err.Error())
		return
	}
	if result.Phrases == nil {
		result.Phrases = []Phrase{}
	}
	w.store.Finish(id, result)
	slog.Info("transcribe job done", "job_id", id, "phrases", len(result.Phrases))
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

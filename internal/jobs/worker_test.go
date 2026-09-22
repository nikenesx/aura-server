package jobs

import (
	"context"
	"testing"
	"time"
)

type fakeRunner struct {
	busy  int
	calls int
}

func (f *fakeRunner) Transcribe(context.Context, string, []byte) (Result, error) {
	f.calls++
	if f.calls <= f.busy {
		return Result{}, ErrBusy
	}
	return Result{Text: "ok"}, nil
}

func newTestWorker(runner Runner, giveUp time.Duration) (*Worker, *Store) {
	store := NewStore(time.Hour)
	w := NewWorker(store, runner)
	w.busyMinDelay = time.Millisecond
	w.busyMaxDelay = 2 * time.Millisecond
	w.busyGiveUp = giveUp
	return w, store
}

func TestWorkerRetriesWhileBusy(t *testing.T) {
	runner := &fakeRunner{busy: 3}
	w, store := newTestWorker(runner, time.Minute)
	store.Create("j1", "a.wav", []byte("x"))

	w.process(context.Background(), "j1")

	got, _ := store.Get("j1")
	if got.Status != StatusDone || runner.calls != 4 {
		t.Fatalf("status=%s calls=%d error=%q", got.Status, runner.calls, got.Error)
	}
}

func TestWorkerGivesUpWhenAlwaysBusy(t *testing.T) {
	runner := &fakeRunner{busy: 1 << 30}
	w, store := newTestWorker(runner, 20*time.Millisecond)
	store.Create("j1", "a.wav", []byte("x"))

	w.process(context.Background(), "j1")

	got, _ := store.Get("j1")
	if got.Status != StatusFailed || got.Error != ErrBusy.Error() || runner.calls < 2 {
		t.Fatalf("status=%s calls=%d error=%q", got.Status, runner.calls, got.Error)
	}
}

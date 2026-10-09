package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nikenesx/aura-server/internal/card"
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

type fakeCards struct {
	err   error
	lines []card.Line
}

func (f *fakeCards) Make(_ context.Context, lines []card.Line) (*card.Card, error) {
	f.lines = lines
	if f.err != nil {
		return nil, f.err
	}
	return &card.Card{Template: "t"}, nil
}

type phrasesRunner struct{}

func (phrasesRunner) Transcribe(context.Context, string, []byte) (Result, error) {
	return Result{Text: "a b", Phrases: []Phrase{{Text: "a", Speaker: "speaker_0"}, {Text: "b", Speaker: "speaker_1"}}}, nil
}

func TestWorkerMakesCardAfterTranscript(t *testing.T) {
	cards := &fakeCards{}
	w, store := newTestWorker(phrasesRunner{}, time.Minute)
	w.EnableCards(cards)
	store.Create("j1", "a.wav", []byte("x"))

	w.process(context.Background(), "j1")
	if got, _ := store.Get("j1"); got.Status != StatusDone || got.CardStatus != StatusQueued {
		t.Fatalf("after transcript: status=%s card=%s", got.Status, got.CardStatus)
	}
	w.makeCard(context.Background(), <-w.cardQueue)

	got, _ := store.Get("j1")
	if got.CardStatus != StatusDone || got.Card == nil || len(cards.lines) != 2 || cards.lines[1].Speaker != "speaker_1" {
		t.Fatalf("card=%s lines=%+v", got.CardStatus, cards.lines)
	}
}

func TestWorkerCardFailureKeepsTranscript(t *testing.T) {
	w, store := newTestWorker(phrasesRunner{}, time.Minute)
	w.EnableCards(&fakeCards{err: errors.New("llm down")})
	store.Create("j1", "a.wav", []byte("x"))

	w.process(context.Background(), "j1")
	w.makeCard(context.Background(), <-w.cardQueue)

	got, _ := store.Get("j1")
	if got.Status != StatusDone || got.CardStatus != StatusFailed || got.CardError != "llm down" {
		t.Fatalf("status=%s card=%s error=%q", got.Status, got.CardStatus, got.CardError)
	}
}

func TestWorkerWithoutCards(t *testing.T) {
	w, store := newTestWorker(phrasesRunner{}, time.Minute)
	store.Create("j1", "a.wav", []byte("x"))
	w.process(context.Background(), "j1")
	if got, _ := store.Get("j1"); got.CardStatus != StatusDisabled {
		t.Fatalf("card=%s", got.CardStatus)
	}
}

func TestWorkerFailedTranscriptFailsCard(t *testing.T) {
	w, store := newTestWorker(&fakeRunner{busy: 1 << 30}, time.Millisecond)
	w.EnableCards(&fakeCards{})
	store.Create("j1", "a.wav", []byte("x"))
	w.process(context.Background(), "j1")
	if got, _ := store.Get("j1"); got.CardStatus != StatusFailed || got.CardError != "transcription failed" {
		t.Fatalf("card=%s error=%q", got.CardStatus, got.CardError)
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

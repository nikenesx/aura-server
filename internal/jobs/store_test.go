package jobs

import (
	"testing"
	"time"
)

func TestStoreLifecycle(t *testing.T) {
	store := NewStore(time.Hour)
	store.Create("j1", "a.wav", []byte("abc"))

	got, ok := store.Get("j1")
	if !ok || got.Status != StatusQueued || got.Audio != nil || got.Bytes != 3 || got.Name != "a.wav" {
		t.Fatalf("queued job: %+v ok=%v", got, ok)
	}

	name, audio, ok := store.TakeAudio("j1")
	if !ok || name != "a.wav" || string(audio) != "abc" {
		t.Fatalf("take audio name=%s audio=%q ok=%v", name, audio, ok)
	}

	got, _ = store.Get("j1")
	if got.Status != StatusProcessing {
		t.Fatalf("status after take: %s", got.Status)
	}

	store.Finish("j1", Result{Text: "hi", Phrases: []Phrase{{Text: "hi", Speaker: "speaker_0"}}})
	got, _ = store.Get("j1")
	if got.Status != StatusDone || got.Result == nil || got.Result.Text != "hi" {
		t.Fatalf("done job: %+v", got)
	}
}

func TestStoreSweep(t *testing.T) {
	store := NewStore(time.Millisecond)
	store.Create("old", "a.wav", []byte("x"))
	time.Sleep(2 * time.Millisecond)
	store.Sweep(time.Now())
	if _, ok := store.Get("old"); ok {
		t.Fatal("expired job should be gone")
	}
}

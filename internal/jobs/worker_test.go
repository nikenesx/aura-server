package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikenesx/aura-server/internal/audio"
	"github.com/nikenesx/aura-server/internal/card"
	"github.com/nikenesx/aura-server/internal/store"
)

type fakeRunner struct {
	busy  int32
	err   error
	calls atomic.Int32
	body  atomic.Value
}

func (f *fakeRunner) Transcribe(_ context.Context, _ string, body io.Reader) (Result, error) {
	n := f.calls.Add(1)
	data, _ := io.ReadAll(body)
	f.body.Store(string(data))
	if n <= f.busy {
		return Result{}, ErrBusy
	}
	if f.err != nil {
		return Result{}, f.err
	}
	return Result{
		Text:     "a b",
		Phrases:  []Phrase{{Text: "a", Speaker: "speaker_0"}, {Text: "b", Speaker: "speaker_1"}},
		Duration: 61.5,
		Waveform: []float32{0.1, 0.9},
	}, nil
}

type fakeCards struct {
	err      error
	rolesErr error
	lines    atomic.Value
	roles    atomic.Value
	// progress, if set, is reported while the card is made.
	progress []float64
}

func (f *fakeCards) Roles(_ context.Context, lines []card.Line) (map[string]string, error) {
	if f.rolesErr != nil {
		return nil, f.rolesErr
	}
	return map[string]string{"speaker_0": card.RoleDoctor, "speaker_1": card.RoleOwner}, nil
}

func (f *fakeCards) Make(_ context.Context, lines []card.Line, roles map[string]string, onProgress func(float64)) (*card.Card, error) {
	f.lines.Store(lines)
	f.roles.Store(roles)
	for _, p := range f.progress {
		onProgress(p)
	}
	if f.err != nil {
		return nil, f.err
	}
	return &card.Card{Template: "vet_appointment_v1", Text: "ok"}, nil
}

type env struct {
	db  *store.DB
	rec *audio.Disk
}

func newEnv(t *testing.T) env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rec, err := audio.NewDisk(filepath.Join(dir, "audio"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser(context.Background(), store.User{ID: "u1", Username: "u1", PasswordHash: "x", DisplayName: "u1", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return env{db: db, rec: rec}
}

func (e env) addRecording(t *testing.T, id string) {
	t.Helper()
	if _, err := e.rec.Put(id+".wav", strings.NewReader("RIFF-"+id), 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := e.db.CreateAppointment(context.Background(), store.Appointment{ID: id, UserID: "u1", CreatedAt: time.Now(), Filename: id + ".wav", AudioKey: id + ".wav", AudioBytes: 9}); err != nil {
		t.Fatal(err)
	}
}

func newTestWorker(e env, runner Runner) *Worker {
	w := NewWorker(e.db, e.rec, runner)
	w.busyMinDelay, w.busyMaxDelay = time.Millisecond, 2*time.Millisecond
	w.idlePoll, w.errorPause, w.progressEvery = 10*time.Millisecond, 10*time.Millisecond, 5*time.Millisecond
	return w
}

// waitFor polls the appointment until check passes.
func waitFor(t *testing.T, db *store.DB, id string, check func(store.Appointment) bool) store.Appointment {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, err := db.Appointment(context.Background(), id, "")
		if err == nil && check(a) {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("appointment %s stuck: %+v (err %v)", id, a, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPipelineMakesTranscriptThenCard(t *testing.T) {
	e := newEnv(t)
	e.addRecording(t, "a1")
	runner, cards := &fakeRunner{}, &fakeCards{}
	w := newTestWorker(e, runner)
	w.EnableCards(cards)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}

	a := waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusDone })
	if runner.body.Load() != "RIFF-a1" {
		t.Fatalf("runner got %q", runner.body.Load())
	}
	var tr Result
	if err := json.Unmarshal(a.Transcript, &tr); err != nil || len(tr.Phrases) != 2 || tr.Waveform != nil {
		t.Fatalf("transcript should hold phrases only: %v %s", err, a.Transcript)
	}
	if a.DurationSec != 61.5 || string(a.Waveform) != "[0.1,0.9]" || a.Progress != 100 || a.Card == nil || a.CardError != "" {
		t.Fatalf("done: %+v", a)
	}
	if lines := cards.lines.Load().([]card.Line); len(lines) != 2 || lines[1].Speaker != "speaker_1" {
		t.Fatalf("card lines: %+v", lines)
	}
	if string(a.Roles) != `{"speaker_0":"doctor","speaker_1":"owner"}` {
		t.Fatalf("roles not stored: %s", a.Roles)
	}
	if roles := cards.roles.Load().(map[string]string); roles["speaker_0"] != card.RoleDoctor {
		t.Fatalf("the card must get the roles: %v", roles)
	}
}

func TestNewWorkWakesTheWorker(t *testing.T) {
	e := newEnv(t)
	w := newTestWorker(e, &fakeRunner{})
	w.idlePoll = time.Hour // only Notify can start it
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = w.Start(ctx)
	time.Sleep(20 * time.Millisecond)

	e.addRecording(t, "a1")
	w.Notify()
	waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusDone })
}

func TestCardsDisabledKeepsTranscript(t *testing.T) {
	e := newEnv(t)
	e.addRecording(t, "a1")
	w := newTestWorker(e, &fakeRunner{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = w.Start(ctx)

	a := waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusDone })
	if a.Transcript == nil || a.Card != nil || a.CardError != CardsDisabled {
		t.Fatalf("%+v", a)
	}
}

func TestCardFailureKeepsTranscript(t *testing.T) {
	e := newEnv(t)
	e.addRecording(t, "a1")
	w := newTestWorker(e, &fakeRunner{})
	w.EnableCards(&fakeCards{err: errors.New("llm down")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = w.Start(ctx)

	a := waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusDone })
	if a.Transcript == nil || a.CardError != "llm down" {
		t.Fatalf("%+v", a)
	}
}

func TestASRFailureFailsAppointment(t *testing.T) {
	e := newEnv(t)
	e.addRecording(t, "a1")
	w := newTestWorker(e, &fakeRunner{err: errors.New("bad audio")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = w.Start(ctx)

	a := waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusFailed })
	if a.Error != "bad audio" {
		t.Fatalf("%+v", a)
	}
}

func TestBusyRunnerIsRetriedWithFreshBody(t *testing.T) {
	e := newEnv(t)
	e.addRecording(t, "a1")
	runner := &fakeRunner{busy: 3}
	w := newTestWorker(e, runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = w.Start(ctx)

	waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusDone })
	// Every attempt must send the whole file, not what a failed one left unread.
	if runner.calls.Load() != 4 || runner.body.Load() != "RIFF-a1" {
		t.Fatalf("calls=%d body=%q", runner.calls.Load(), runner.body.Load())
	}
}

func TestRestartResumesInterruptedWork(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addRecording(t, "cut")
	_ = e.db.SetStatus(ctx, "cut", store.StatusTranscribing, 20)

	e.addRecording(t, "card")
	_ = e.db.SaveTranscript(ctx, "card", json.RawMessage(`{"text":"x","phrases":[{"text":"x","speaker":"speaker_0"}]}`), nil, 1)
	_ = e.db.SetStatus(ctx, "card", store.StatusWritingCard, 50)

	w := newTestWorker(e, &fakeRunner{})
	w.EnableCards(&fakeCards{})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	_ = w.Start(runCtx)

	for _, id := range []string{"cut", "card"} {
		a := waitFor(t, e.db, id, func(a store.Appointment) bool { return a.Status == store.StatusDone })
		if a.Card == nil {
			t.Fatalf("%s: no card after resume", id)
		}
	}
}

func TestRolesFailureStillMakesCard(t *testing.T) {
	e := newEnv(t)
	e.addRecording(t, "a1")
	cards := &fakeCards{rolesErr: errors.New("llm hiccup")}
	w := newTestWorker(e, &fakeRunner{})
	w.EnableCards(cards)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = w.Start(ctx)

	a := waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusDone })
	if a.Card == nil || a.Roles != nil {
		t.Fatalf("%+v", a)
	}
	if roles, _ := cards.roles.Load().(map[string]string); len(roles) != 0 {
		t.Fatalf("no roles should reach the card: %v", roles)
	}
}

// progressRunner reports ASR progress while a test holds the transcription open.
type progressRunner struct {
	fakeRunner
	release chan struct{}
}

func (p *progressRunner) Transcribe(ctx context.Context, name string, body io.Reader) (Result, error) {
	<-p.release
	return p.fakeRunner.Transcribe(ctx, name, body)
}

func (p *progressRunner) Progress(context.Context) (float64, bool, error) {
	return 0.5, true, nil
}

func TestProgressFollowsStages(t *testing.T) {
	e := newEnv(t)
	e.addRecording(t, "a1")
	runner := &progressRunner{release: make(chan struct{})}
	cards := &fakeCards{progress: []float64{0.2, 0.6, 0.6, 0.95}}
	w := newTestWorker(e, runner)
	w.EnableCards(cards)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = w.Start(ctx)

	// Half way through ASR is half way between its milestones.
	mid := (ProgressTranscribing + ProgressTranscribed) / 2
	waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Progress >= mid-1 && a.Status == store.StatusTranscribing })
	close(runner.release)

	a := waitFor(t, e.db, "a1", func(a store.Appointment) bool { return a.Status == store.StatusDone })
	if a.Progress != 100 {
		t.Fatalf("done must be 100: %d", a.Progress)
	}
}

func TestScale(t *testing.T) {
	cases := []struct {
		f      float64
		lo, hi int
		want   int
	}{
		{0, 50, 99, 50}, {1, 50, 99, 99}, {0.5, 2, 45, 23}, {-1, 2, 45, 2}, {7, 2, 45, 45},
	}
	for _, c := range cases {
		if got := scale(c.f, c.lo, c.hi); got != c.want {
			t.Errorf("scale(%v, %d, %d) = %d, want %d", c.f, c.lo, c.hi, got, c.want)
		}
	}
}

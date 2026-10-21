package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func addUser(t *testing.T, db *DB, id string) {
	t.Helper()
	if err := db.CreateUser(context.Background(), User{ID: id, Username: id, PasswordHash: "x", DisplayName: id, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsRunOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 0; i < 2; i++ {
		db, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		_ = db.Close()
	}
}

func TestSessionExpiry(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	addUser(t, db, "u1")
	now := time.Now()
	if err := db.CreateSession(ctx, "live", "u1", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "old", "u1", now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if u, err := db.SessionUser(ctx, "live", now); err != nil || u.ID != "u1" {
		t.Fatalf("live session: %v %+v", err, u)
	}
	if _, err := db.SessionUser(ctx, "old", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if err := db.DeleteSession(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionUser(ctx, "live", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session: %v", err)
	}
}

func TestAppointmentLifecycle(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	addUser(t, db, "u1")
	addUser(t, db, "u2")
	if err := db.CreateAppointment(ctx, Appointment{ID: "a1", UserID: "u1", CreatedAt: time.Now(), Filename: "rec.m4a", AudioKey: "a1.m4a", AudioBytes: 10}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Appointment(ctx, "a1", "u2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's appointment: %v", err)
	}
	a, err := db.Appointment(ctx, "a1", "u1")
	if err != nil || a.Status != StatusQueued || a.Card != nil {
		t.Fatalf("new appointment: %v %+v", err, a)
	}

	next, err := db.Next(ctx, StatusQueued)
	if err != nil || next.ID != "a1" {
		t.Fatalf("next queued: %v %+v", err, next)
	}

	if err := db.SetStatus(ctx, "a1", StatusTranscribing, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.SetProgress(ctx, "a1", 30); err != nil {
		t.Fatal(err)
	}
	if err := db.SetProgress(ctx, "a1", 10); err != nil {
		t.Fatal(err)
	}
	if a, _ := db.Appointment(ctx, "a1", ""); a.Progress != 30 {
		t.Fatalf("progress must not go back: %d", a.Progress)
	}

	if err := db.SaveTranscript(ctx, "a1", json.RawMessage(`{"text":"hi","phrases":[]}`), json.RawMessage(`[0.5]`), 12.5); err != nil {
		t.Fatal(err)
	}
	card := json.RawMessage(`{"template":"t","sections":[]}`)
	if err := db.FinishCard(ctx, "a1", card); err != nil {
		t.Fatal(err)
	}
	a, _ = db.Appointment(ctx, "a1", "u1")
	if a.Status != StatusDone || a.Progress != 100 || a.DurationSec != 12.5 || string(a.Card) != string(card) || a.CardEditedAt != nil {
		t.Fatalf("finished: %+v", a)
	}

	edited := json.RawMessage(`{"template":"t","sections":[],"text":"edited"}`)
	if err := db.SaveCardEdit(ctx, "a1", "u2", edited); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's edit: %v", err)
	}
	if err := db.SaveCardEdit(ctx, "a1", "u1", edited); err != nil {
		t.Fatal(err)
	}
	a, _ = db.Appointment(ctx, "a1", "u1")
	if string(a.Card) != string(edited) || string(a.CardGenerated) != string(card) || a.CardEditedAt == nil {
		t.Fatalf("edit should keep the model's version apart: %+v", a)
	}

	if err := db.RestartCard(ctx, "a1", "u1", 50); err != nil {
		t.Fatal(err)
	}
	if a, _ := db.Appointment(ctx, "a1", ""); a.Status != StatusWritingCard {
		t.Fatalf("restarted: %s", a.Status)
	}
	if err := db.RestartCard(ctx, "a1", "u1", 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restart while writing should be refused: %v", err)
	}
}

func TestListPagesNewestFirst(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	addUser(t, db, "u1")
	addUser(t, db, "u2")
	base := time.Now().Add(-time.Hour)
	for i, id := range []string{"a1", "a2", "a3"} {
		if err := db.CreateAppointment(ctx, Appointment{ID: id, UserID: "u1", CreatedAt: base.Add(time.Duration(i) * time.Minute), Filename: id, AudioKey: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateAppointment(ctx, Appointment{ID: "other", UserID: "u2", CreatedAt: base, Filename: "x", AudioKey: "x"}); err != nil {
		t.Fatal(err)
	}

	page, err := db.ListAppointments(ctx, "u1", time.Time{}, 2)
	if err != nil || len(page) != 2 || page[0].ID != "a3" || page[1].ID != "a2" {
		t.Fatalf("first page: %v %+v", err, page)
	}
	page, err = db.ListAppointments(ctx, "u1", page[1].CreatedAt, 2)
	if err != nil || len(page) != 1 || page[0].ID != "a1" {
		t.Fatalf("second page: %v %+v", err, page)
	}
}

func TestResetInterrupted(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	addUser(t, db, "u1")
	_ = db.CreateAppointment(ctx, Appointment{ID: "a1", UserID: "u1", CreatedAt: time.Now(), Filename: "x", AudioKey: "x"})
	_ = db.SetStatus(ctx, "a1", StatusTranscribing, 20)

	n, err := db.ResetInterrupted(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reset: %v %d", err, n)
	}
	if a, _ := db.Appointment(ctx, "a1", ""); a.Status != StatusQueued || a.Progress != 0 {
		t.Fatalf("after reset: %+v", a)
	}
}

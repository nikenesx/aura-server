package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Status is where an appointment is in the pipeline.
type Status string

const (
	StatusQueued       Status = "queued"
	StatusTranscribing Status = "transcribing"
	StatusRoles        Status = "roles"
	StatusWritingCard  Status = "writing_card"
	// StatusDone means the transcript is ready. The card is ready too unless
	// CardError says why it is not.
	StatusDone Status = "done"
	// StatusFailed means the transcript could not be made.
	StatusFailed Status = "failed"
)

type Appointment struct {
	ID          string
	UserID      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Filename    string
	AudioKey    string
	AudioBytes  int64
	DurationSec float64
	Status      Status
	Progress    int
	Error       string
	// JSON documents; nil when not made yet.
	Transcript    json.RawMessage
	Roles         json.RawMessage
	Waveform      json.RawMessage
	CardGenerated json.RawMessage
	Card          json.RawMessage
	CardEditedAt  *time.Time
	CardError     string
}

const appointmentColumns = `id, user_id, created_at, updated_at, filename, audio_key, audio_bytes, duration_sec,
	status, progress, error, transcript, roles, waveform, card_generated, card, card_edited_at, card_error`

func (s *DB) CreateAppointment(ctx context.Context, a Appointment) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appointments (id, user_id, created_at, updated_at, filename, audio_key, audio_bytes, status, progress)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		a.ID, a.UserID, ms(a.CreatedAt), ms(a.CreatedAt), a.Filename, a.AudioKey, a.AudioBytes, StatusQueued,
	)
	return err
}

// Appointment returns an appointment of the given user; any user's when userID is empty.
func (s *DB) Appointment(ctx context.Context, id, userID string) (Appointment, error) {
	query := `SELECT ` + appointmentColumns + ` FROM appointments WHERE id = ?`
	args := []any{id}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Appointment{}, err
	}
	list, err := scanAppointments(rows)
	if err != nil {
		return Appointment{}, err
	}
	if len(list) == 0 {
		return Appointment{}, ErrNotFound
	}
	return list[0], nil
}

// ListAppointments returns a user's appointments newest first, older than
// before when it is set. Transcript and waveform are left out: the history
// list does not need them and they are the bulk of a row.
func (s *DB) ListAppointments(ctx context.Context, userID string, before time.Time, limit int) ([]Appointment, error) {
	query := `SELECT id, user_id, created_at, updated_at, filename, audio_key, audio_bytes, duration_sec,
		status, progress, error, NULL, roles, NULL, card_generated, card, card_edited_at, card_error
		FROM appointments WHERE user_id = ?`
	args := []any{userID}
	if !before.IsZero() {
		query += ` AND created_at < ?`
		args = append(args, ms(before))
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanAppointments(rows)
}

// Next returns the oldest appointment in any of the given statuses, for the
// worker to pick up. ErrNotFound means there is nothing to do.
func (s *DB) Next(ctx context.Context, statuses ...Status) (Appointment, error) {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(statuses)), ", ")
	args := make([]any, len(statuses))
	for i, st := range statuses {
		args[i] = st
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+appointmentColumns+` FROM appointments WHERE status IN (`+marks+`) ORDER BY created_at LIMIT 1`, args...)
	if err != nil {
		return Appointment{}, err
	}
	list, err := scanAppointments(rows)
	if err != nil {
		return Appointment{}, err
	}
	if len(list) == 0 {
		return Appointment{}, ErrNotFound
	}
	return list[0], nil
}

// ResetInterrupted puts appointments a restart cut off mid-transcription back
// in the queue. Card generation needs no reset: its status already says what
// to redo.
func (s *DB) ResetInterrupted(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE appointments SET status = ?, progress = 0, updated_at = ? WHERE status = ?`,
		StatusQueued, ms(time.Now()), StatusTranscribing)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *DB) SetStatus(ctx context.Context, id string, status Status, progress int) error {
	return s.exec(ctx, `UPDATE appointments SET status = ?, progress = ?, updated_at = ? WHERE id = ?`,
		status, progress, ms(time.Now()), id)
}

// SetProgress only moves progress forward, so a late or stale update never
// makes the bar jump back.
func (s *DB) SetProgress(ctx context.Context, id string, progress int) error {
	return s.exec(ctx,
		`UPDATE appointments SET progress = CASE WHEN ? > progress THEN ? ELSE progress END, updated_at = ? WHERE id = ?`,
		progress, progress, ms(time.Now()), id)
}

func (s *DB) SaveTranscript(ctx context.Context, id string, transcript, waveform json.RawMessage, durationSec float64) error {
	return s.exec(ctx,
		`UPDATE appointments SET transcript = ?, waveform = ?, duration_sec = ?, updated_at = ? WHERE id = ?`,
		nullJSON(transcript), nullJSON(waveform), durationSec, ms(time.Now()), id)
}

func (s *DB) SaveRoles(ctx context.Context, id string, roles json.RawMessage) error {
	return s.exec(ctx, `UPDATE appointments SET roles = ?, updated_at = ? WHERE id = ?`,
		nullJSON(roles), ms(time.Now()), id)
}

// FinishCard stores the model's card and marks the appointment done.
func (s *DB) FinishCard(ctx context.Context, id string, card json.RawMessage) error {
	return s.exec(ctx,
		`UPDATE appointments SET card_generated = ?, card = ?, card_edited_at = NULL, card_error = '',
		 status = ?, progress = 100, updated_at = ? WHERE id = ?`,
		nullJSON(card), nullJSON(card), StatusDone, ms(time.Now()), id)
}

// FailCard marks the appointment done without a card: the transcript stands.
func (s *DB) FailCard(ctx context.Context, id, message string) error {
	return s.exec(ctx,
		`UPDATE appointments SET card_error = ?, status = ?, progress = 100, updated_at = ? WHERE id = ?`,
		message, StatusDone, ms(time.Now()), id)
}

// Fail marks the appointment failed: there is no transcript.
func (s *DB) Fail(ctx context.Context, id, message string) error {
	return s.exec(ctx, `UPDATE appointments SET error = ?, status = ?, updated_at = ? WHERE id = ?`,
		message, StatusFailed, ms(time.Now()), id)
}

// SaveCardEdit stores the doctor's version of the card; the model's stays in card_generated.
func (s *DB) SaveCardEdit(ctx context.Context, id, userID string, card json.RawMessage) error {
	now := ms(time.Now())
	return s.exec(ctx,
		`UPDATE appointments SET card = ?, card_edited_at = ?, updated_at = ? WHERE id = ? AND user_id = ? AND card IS NOT NULL`,
		nullJSON(card), now, now, id, userID)
}

// RestartCard sends a finished appointment back for a new card. The doctor's
// edits are dropped: they asked for a fresh one.
func (s *DB) RestartCard(ctx context.Context, id, userID string, progress int) error {
	return s.exec(ctx,
		`UPDATE appointments SET status = ?, progress = ?, card_error = '', updated_at = ?
		 WHERE id = ? AND user_id = ? AND status = ? AND transcript IS NOT NULL`,
		StatusWritingCard, progress, ms(time.Now()), id, userID, StatusDone)
}

func (s *DB) exec(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func nullJSON(v json.RawMessage) any {
	if len(v) == 0 || strings.TrimSpace(string(v)) == "null" {
		return nil
	}
	return string(v)
}

func scanAppointments(rows *sql.Rows) ([]Appointment, error) {
	defer rows.Close()
	var out []Appointment
	for rows.Next() {
		var a Appointment
		var created, updated int64
		var duration sql.NullFloat64
		var status string
		var transcript, roles, waveform, cardGenerated, card sql.NullString
		var edited sql.NullInt64
		if err := rows.Scan(&a.ID, &a.UserID, &created, &updated, &a.Filename, &a.AudioKey, &a.AudioBytes, &duration,
			&status, &a.Progress, &a.Error, &transcript, &roles, &waveform, &cardGenerated, &card, &edited, &a.CardError); err != nil {
			return nil, err
		}
		a.CreatedAt, a.UpdatedAt = fromMS(created), fromMS(updated)
		a.DurationSec = duration.Float64
		a.Status = Status(status)
		a.Transcript, a.Roles, a.Waveform = raw(transcript), raw(roles), raw(waveform)
		a.CardGenerated, a.Card = raw(cardGenerated), raw(card)
		if edited.Valid {
			t := fromMS(edited.Int64)
			a.CardEditedAt = &t
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		return []Appointment{}, nil
	}
	return out, nil
}

func raw(v sql.NullString) json.RawMessage {
	if !v.Valid {
		return nil
	}
	return json.RawMessage(v.String)
}

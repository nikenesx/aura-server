package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID           string
	Username     string
	PasswordHash string
	DisplayName  string
	CreatedAt    time.Time
}

func (s *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *DB) CreateUser(ctx context.Context, u User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, display_name, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.DisplayName, ms(u.CreatedAt),
	)
	return err
}

func (s *DB) UserByUsername(ctx context.Context, username string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, display_name, created_at FROM users WHERE username = ?`, username))
}

func (s *DB) CreateSession(ctx context.Context, tokenHash, userID string, created, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, ms(created), ms(expires),
	)
	return err
}

// SessionUser returns the owner of a session that has not expired by now.
func (s *DB) SessionUser(ctx context.Context, tokenHash string, now time.Time) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.password_hash, u.display_name, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash, ms(now)))
}

func (s *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, ms(now))
	return err
}

func (s *DB) scanUser(row *sql.Row) (User, error) {
	var u User
	var created int64
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = fromMS(created)
	return u, nil
}

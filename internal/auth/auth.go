// Package auth signs doctors in and checks their session on every request.
//
// Sessions are opaque random tokens. Only their SHA-256 is stored, so a leaked
// database does not hand out working tokens, and logging out revokes at once.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/nikenesx/aura-server/internal/store"
)

var ErrInvalidCredentials = errors.New("invalid username or password")

type Users interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u store.User) error
	UserByUsername(ctx context.Context, username string) (store.User, error)
	CreateSession(ctx context.Context, tokenHash, userID string, created, expires time.Time) error
	SessionUser(ctx context.Context, tokenHash string, now time.Time) (store.User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
}

type Service struct {
	users Users
	ttl   time.Duration
	// decoy is checked against when the username is unknown, so a wrong
	// username takes as long as a wrong password and names cannot be probed.
	decoy []byte
}

func NewService(users Users, ttl time.Duration) (*Service, error) {
	decoy, err := bcrypt.GenerateFromPassword([]byte(uuid.NewString()), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &Service{users: users, ttl: ttl, decoy: decoy}, nil
}

// Bootstrap creates the first account when there are none and reports
// whether it did.
func (s *Service) Bootstrap(ctx context.Context, username, password string) (bool, error) {
	n, err := s.users.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	_, err = s.CreateUser(ctx, username, password)
	return err == nil, err
}

// CreateUser adds a doctor's account.
func (s *Service) CreateUser(ctx context.Context, username, password string) (store.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return store.User{}, errors.New("username and password must not be empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return store.User{}, err
	}
	u := store.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: string(hash),
		DisplayName:  username,
		CreatedAt:    time.Now(),
	}
	return u, s.users.CreateUser(ctx, u)
}

func (s *Service) Login(ctx context.Context, username, password string) (token string, user store.User, expires time.Time, err error) {
	user, err = s.users.UserByUsername(ctx, strings.TrimSpace(username))
	if errors.Is(err, store.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(s.decoy, []byte(password))
		return "", store.User{}, time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", store.User{}, time.Time{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", store.User{}, time.Time{}, ErrInvalidCredentials
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", store.User{}, time.Time{}, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	expires = now.Add(s.ttl)
	if err := s.users.CreateSession(ctx, hashToken(token), user.ID, now, expires); err != nil {
		return "", store.User{}, time.Time{}, fmt.Errorf("create session: %w", err)
	}
	return token, user, expires, nil
}

// Authenticate returns the user a live session token belongs to.
func (s *Service) Authenticate(ctx context.Context, token string) (store.User, error) {
	if token == "" {
		return store.User{}, ErrInvalidCredentials
	}
	user, err := s.users.SessionUser(ctx, hashToken(token), time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrInvalidCredentials
	}
	return user, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	return s.users.DeleteSession(ctx, hashToken(token))
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

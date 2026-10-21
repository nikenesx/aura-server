package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nikenesx/aura-server/internal/httpx"
	"github.com/nikenesx/aura-server/internal/store"
)

type ctxKey struct{}

// UserFrom returns the signed-in user Require put into the request context.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(store.User)
	return u, ok
}

// Require rejects requests without a live session with 401.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.Authenticate(r.Context(), bearer(r))
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err != nil {
			slog.Error("authenticate", "error", err)
			httpx.WriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

type userView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

func view(u store.User) userView {
	return userView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName}
}

func (s *Service) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	token, user, expires, err := s.Login(r.Context(), body.Username, body.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err != nil {
		slog.Error("login", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"user":       view(user),
	})
}

func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Logout(r.Context(), bearer(r)); err != nil {
		slog.Error("logout", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFrom(r.Context())
	httpx.WriteJSON(w, http.StatusOK, view(user))
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

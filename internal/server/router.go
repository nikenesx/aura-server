package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nikenesx/aura-server/internal/appointments"
	"github.com/nikenesx/aura-server/internal/auth"
	"github.com/nikenesx/aura-server/internal/health"
)

func NewRouter(healthHandler *health.Handler, authService *auth.Service, appointmentsHandler *appointments.Handler) http.Handler {
	r := chi.NewRouter()

	for _, mw := range middlewareStack() {
		r.Use(mw)
	}

	r.Get("/health", healthHandler.ServeHTTP)
	r.Post("/v1/auth/login", authService.HandleLogin)

	r.Group(func(r chi.Router) {
		r.Use(authService.Require)
		r.Post("/v1/auth/logout", authService.HandleLogout)
		r.Get("/v1/auth/me", authService.HandleMe)
		appointmentsHandler.Routes(r)
	})

	return r
}

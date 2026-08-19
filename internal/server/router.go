package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nikenesx/aura-server/internal/health"
)

func NewRouter(healthHandler *health.Handler) http.Handler {
	r := chi.NewRouter()

	for _, mw := range middlewareStack() {
		r.Use(mw)
	}

	r.Get("/health", healthHandler.ServeHTTP)

	return r
}

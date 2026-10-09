package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nikenesx/aura-server/internal/health"
	"github.com/nikenesx/aura-server/internal/transcribe"
)

func NewRouter(healthHandler *health.Handler, transcribeHandler *transcribe.Handler) http.Handler {
	r := chi.NewRouter()

	for _, mw := range middlewareStack() {
		r.Use(mw)
	}

	r.Get("/health", healthHandler.ServeHTTP)
	r.Post("/v1/transcribe", transcribeHandler.Create)
	r.Get("/v1/transcribe/{jobID}", transcribeHandler.Get)
	r.Get("/v1/transcribe/{jobID}/card", transcribeHandler.Card)

	return r
}

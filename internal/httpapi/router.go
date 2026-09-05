package httpapi

import (
	"net/http"

	"github.com/cschleiden/go-workflows/diag"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter creates the router with all routes.
func NewRouter(h *Handlers, diagBackend diag.Backend, restrictDiag bool) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	// Health check
	r.Get("/healthz", h.Healthz)

	if diagBackend != nil && !restrictDiag {
		r.Get("/diag", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/diag/", http.StatusMovedPermanently)
		})
		r.Mount("/diag/", http.StripPrefix("/diag", diag.NewServeMux(diagBackend)))
	}

	// API routes
	r.Route("/v1", func(r chi.Router) {
		r.Get("/models", h.ListModels)
		r.Post("/summaries", h.CreateSummary)
		r.Get("/summaries/{run_id}", h.GetSummary)
		r.Get("/runs/{run_id}/status", h.GetRunStatus)
		r.Delete("/runs/{run_id}", h.CancelRun)
		r.Patch("/runs/{run_id}", h.UpdateRun)
	})

	return r
}

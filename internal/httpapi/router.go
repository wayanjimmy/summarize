package httpapi

import (
	"log"
	"net/http"
	"os"

	"github.com/cschleiden/go-workflows/diag"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	agentfeedback "github.com/open-software-network/os-epode/sdk/go"
)

// NewRouter creates the router with all routes and Agent Feedback middleware.
func NewRouter(h *Handlers, diagBackend diag.Backend) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	// Health check
	r.Get("/healthz", h.Healthz)

	if diagBackend != nil {
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
	})

	apiKey := os.Getenv("AGENT_FEEDBACK_KEY")
	if apiKey != "" {
		feedback, err := agentfeedback.New(agentfeedback.Options{
			APIKey:  apiKey,
			Include: []string{"/v1/summaries"},
		})
		if err != nil {
			log.Fatal(err)
		}
		return feedback.Middleware(r)
	}

	return r
}


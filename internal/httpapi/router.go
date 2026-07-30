package httpapi

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/cschleiden/go-workflows/diag"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	agentfeedback "github.com/open-software-network/os-epode/sdk/go"
)

type agentFeedbackSessionKey struct{}

type agentFeedbackSession struct {
	ref string
}

func withAgentFeedbackSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := &agentFeedbackSession{}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentFeedbackSessionKey{}, session)))
	})
}

func setAgentFeedbackSession(r *http.Request, ref string) {
	if session, ok := r.Context().Value(agentFeedbackSessionKey{}).(*agentFeedbackSession); ok {
		session.ref = ref
	}
}

func agentFeedbackSessionRef(r *http.Request) string {
	if session, ok := r.Context().Value(agentFeedbackSessionKey{}).(*agentFeedbackSession); ok {
		return session.ref
	}
	return ""
}

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
		opts := agentfeedback.Options{
			APIKey:     apiKey,
			Include:    []string{"/v1/summaries", "/v1/summaries/**", "/v1/runs/**/status"},
			SessionRef: agentFeedbackSessionRef,
		}
		if endpoint := os.Getenv("AGENT_FEEDBACK_ENDPOINT"); endpoint != "" {
			opts.Endpoint = endpoint
		}
		feedback, err := agentfeedback.New(opts)
		if err != nil {
			log.Fatal(err)
		}
		return withAgentFeedbackSession(feedback.Middleware(r))
	}

	return r
}

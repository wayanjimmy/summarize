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
// If mcpHandler is non-nil, it is mounted at POST /mcp outside the Agent
// Feedback middleware surface. If restrictDiag is true, the /diag endpoint
// is not mounted (for production deployments with auth enabled).
type AgentFeedbackConfig struct{ APIKey, Endpoint, RuntimeHint, AnonymousRef string }

func NewRouter(h *Handlers, mcpHandler http.Handler, diagBackend diag.Backend, restrictDiag bool, feedbackCfg ...AgentFeedbackConfig) http.Handler {
	r, _ := NewRouterWithShutdown(h, mcpHandler, diagBackend, restrictDiag, feedbackCfg...)
	return r
}

func NewRouterWithShutdown(h *Handlers, mcpHandler http.Handler, diagBackend diag.Backend, restrictDiag bool, feedbackCfg ...AgentFeedbackConfig) (http.Handler, func(context.Context) error) {
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

	// MCP endpoint (stateless 2026-07-28 Streamable HTTP).
	// Register for all methods so the MCP handler itself can return the
	// spec-compliant 405 with an Allow header for non-POST probes (e.g. GET
	// for standalone SSE discovery). Chi's own 405 lacks the Allow header.
	if mcpHandler != nil {
		r.Handle("/mcp", mcpHandler)
	}

	var af AgentFeedbackConfig
	if len(feedbackCfg) > 0 {
		af = feedbackCfg[0]
	} else {
		af.APIKey = os.Getenv("AGENT_FEEDBACK_KEY")
		af.Endpoint = os.Getenv("AGENT_FEEDBACK_ENDPOINT")
	}
	apiKey := af.APIKey
	if apiKey != "" {
		opts := agentfeedback.Options{
			APIKey:       apiKey,
			Include:      []string{"/v1/summaries", "/v1/summaries/**", "/v1/runs/**"},
			SessionRef:   agentFeedbackSessionRef,
			AnonymousRef: func(_ *http.Request) string { return af.AnonymousRef },
			RuntimeHint:  func(_ *http.Request) string { return af.RuntimeHint },
			CacheMode:    agentfeedback.CachePrivate,
		}
		if endpoint := af.Endpoint; endpoint != "" {
			opts.Endpoint = endpoint
		}
		feedback, err := agentfeedback.New(opts)
		if err != nil {
			log.Fatal(err)
		}
		return withAgentFeedbackSession(feedback.Middleware(r)), feedback.Shutdown
	}

	return r, func(context.Context) error { return nil }
}

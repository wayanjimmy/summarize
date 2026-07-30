package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/summary"
)

// createSummaryRequest is the JSON body for POST /v1/summaries.
type createSummaryRequest struct {
	URL    string `json:"url"`
	Text   string `json:"text"`
	Engine string `json:"engine"`
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Format string `json:"format"` // "summary", "chapters", "thread", "blog"
}

// updateRunRequest is the JSON body for PATCH /v1/runs/{run_id}.
type updateRunRequest struct {
	Prompt string `json:"prompt"`
}

// Handlers holds the HTTP handler dependencies.
type Handlers struct {
	Service *summary.Service
}

// Healthz handles GET /healthz.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ListModels handles GET /v1/models.
func (h *Handlers) ListModels(w http.ResponseWriter, r *http.Request) {
	result, err := h.Service.ListModels(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := ModelsResponse{Engines: map[string]EngineModelsResponse{}}
	for _, name := range []string{domain.EnginePi, domain.EngineAgy} {
		info := result.Engines[name]
		entry := EngineModelsResponse{
			Models:       info.Models,
			DefaultModel: info.DefaultModel,
			Status:       info.Status,
			Available:    info.Available,
			Stale:        info.Stale,
			Error:        info.Error,
		}
		if !info.FetchedAt.IsZero() {
			entry.FetchedAt = info.FetchedAt.UTC().Format(time.RFC3339)
		}
		resp.Engines[name] = entry
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateSummary handles POST /v1/summaries.
func (h *Handlers) CreateSummary(w http.ResponseWriter, r *http.Request) {
	var req createSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}

	result, err := h.Service.Submit(r.Context(), summary.SubmitRequest{
		URL:     req.URL,
		Text:    req.Text,
		Engine:  req.Engine,
		Model:   req.Model,
		Prompt:  req.Prompt,
		Format:  req.Format,
		OwnerID: "local",
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	setAgentFeedbackSession(r, result.RunID)

	statusCode := http.StatusAccepted
	if result.Cached && result.Status == domain.StatusSucceeded {
		statusCode = http.StatusOK
	}

	writeJSON(w, statusCode, CreateSummaryResponse{
		RunID:     result.RunID,
		Status:    result.Status,
		StatusURL: "/v1/runs/" + result.RunID + "/status",
		ResultURL: "/v1/summaries/" + result.RunID,
		Cached:    result.Cached,
	})
}

// GetRunStatus handles GET /v1/runs/{run_id}/status.
func (h *Handlers) GetRunStatus(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "run_id is required")
		return
	}

	run, err := h.Service.Get(r.Context(), runID, "local")
	if err != nil {
		writeServiceError(w, err)
		return
	}
	setAgentFeedbackSession(r, run.ID)

	resp := StatusResponse{
		RunID:     run.ID,
		Status:    run.Status,
		Stage:     run.Stage,
		CreatedAt: run.CreatedAt.Format(time.RFC3339),
		UpdatedAt: run.UpdatedAt.Format(time.RFC3339),
	}

	if run.ErrorCode != "" {
		resp.Error = &domain.RunError{
			Code:    run.ErrorCode,
			Message: run.ErrorMessage,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetSummary handles GET /v1/summaries/{run_id}.
func (h *Handlers) GetSummary(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "run_id is required")
		return
	}

	run, err := h.Service.Get(r.Context(), runID, "local")
	if err != nil {
		writeServiceError(w, err)
		return
	}
	setAgentFeedbackSession(r, run.ID)

	resp := SummaryResponse{
		RunID:           run.ID,
		Status:          run.Status,
		Stage:           run.Stage,
		InputType:       run.InputType,
		SourceURL:       run.SourceURL,
		Engine:          run.Engine,
		Prompt:          run.Prompt,
		Format:          run.Format,
		Truncated:       run.Truncated,
		Summary:         run.Summary,
		TranscriptChars: run.TranscriptChars,
		SummaryChars:    run.SummaryChars,
		CreatedAt:       run.CreatedAt.Format(time.RFC3339),
	}

	if run.InputType == domain.InputTypeYouTube && run.YouTubeVideoID != "" {
		resp.YouTube = &YouTubeInfo{
			VideoID: run.YouTubeVideoID,
			Title:   run.YouTubeTitle,
		}
	}

	if !run.StartedAt.IsZero() {
		resp.StartedAt = run.StartedAt.Format(time.RFC3339)
	}
	if !run.FinishedAt.IsZero() {
		resp.FinishedAt = run.FinishedAt.Format(time.RFC3339)
	}

	if run.ErrorCode != "" {
		resp.Error = &domain.RunError{
			Code:    run.ErrorCode,
			Message: run.ErrorMessage,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// CancelRun handles DELETE /v1/runs/{run_id}.
func (h *Handlers) CancelRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "run_id is required")
		return
	}

	task, err := h.Service.Cancel(r.Context(), runID, "local")
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, TaskResponse{
		RunID:   task.ID,
		Status:  task.Status,
		Stage:   task.Stage,
		Message: "run cancelled",
	})
}

// UpdateRun handles PATCH /v1/runs/{run_id}.
func (h *Handlers) UpdateRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "run_id is required")
		return
	}

	var req updateRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return
	}

	task, err := h.Service.Update(r.Context(), summary.UpdateRequest{
		RunID:   runID,
		OwnerID: "local",
		Prompt:  req.Prompt,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, TaskResponse{
		RunID:  task.ID,
		Status: task.Status,
		Stage:  task.Stage,
	})
}

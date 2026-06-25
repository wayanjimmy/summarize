package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jimbo/summarize/internal/config"
	"github.com/jimbo/summarize/internal/domain"
	"github.com/jimbo/summarize/internal/engine"
	"github.com/jimbo/summarize/internal/events"
	"github.com/jimbo/summarize/internal/store"
	"github.com/jimbo/summarize/internal/youtube"
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

// Handlers holds the HTTP handler dependencies.
type Handlers struct {
	Store     *store.Store
	Config    *config.Config
	Publisher *events.Publisher
	Models    *engine.ModelCatalog
}

// Healthz handles GET /healthz.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ListModels handles GET /v1/models.
func (h *Handlers) ListModels(w http.ResponseWriter, r *http.Request) {
	if h.Models == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "model catalog unavailable")
		return
	}

	listed := h.Models.ListAll(r.Context())
	resp := ModelsResponse{Engines: map[string]EngineModelsResponse{}}
	for _, name := range []string{domain.EnginePi, domain.EngineAgy} {
		models := listed[name]
		status := "available"
		available := models.Error == "" && len(models.Models) > 0
		if !available {
			status = "unavailable"
		}
		if models.Stale {
			status = "stale"
			available = true
		}
		entry := EngineModelsResponse{
			Models:       models.Models,
			DefaultModel: h.defaultModel(name),
			Status:       status,
			Available:    available,
			Stale:        models.Stale,
			Error:        models.Error,
		}
		if !models.FetchedAt.IsZero() {
			entry.FetchedAt = models.FetchedAt.UTC().Format(time.RFC3339)
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

	// Validate: exactly one of url or text
	if (req.URL == "" && req.Text == "") || (req.URL != "" && req.Text != "") {
		writeError(w, http.StatusBadRequest, "invalid_request", "exactly one of url or text is required")
		return
	}

	// Determine input type
	var inputType string
	if req.URL != "" {
		if !youtube.IsValidURL(req.URL) {
			writeError(w, http.StatusBadRequest, "invalid_request", "url must be a valid YouTube URL")
			return
		}
		inputType = domain.InputTypeYouTube
	} else {
		if strings.TrimSpace(req.Text) == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "text must be non-empty")
			return
		}
		inputType = domain.InputTypeText
	}

	// Validate format
	format := req.Format
	if format == "" {
		format = domain.FormatSummary // default
	}
	if !domain.ValidFormats[format] {
		writeError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("format must be one of: summary, chapters, thread, blog"))
		return
	}

	// Validate format+input compatibility
	if format == domain.FormatChapters && inputType == domain.InputTypeText {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"format 'chapters' requires a YouTube URL")
		return
	}

	// Resolve engine
	engine := h.Config.DefaultEngine
	if req.Engine != "" {
		if req.Engine != domain.EnginePi && req.Engine != domain.EngineAgy {
			writeError(w, http.StatusBadRequest, "invalid_request", "engine must be 'pi' or 'agy'")
			return
		}
		engine = req.Engine
	}

	model, status, err := h.resolveModel(r.Context(), engine, req.Model)
	if err != nil {
		if status == 0 {
			status = http.StatusBadRequest
		}
		code := "invalid_request"
		if status == http.StatusServiceUnavailable {
			code = "service_unavailable"
		}
		writeError(w, status, code, err.Error())
		return
	}

	// Resolve prompt
	// For non-summary formats, the format template overrides the default prompt.
	// The user's prompt becomes a refinement appended to the format template.
	// For summary format, keep existing behavior: use DefaultPrompt unless overridden.
	promptText := ""
	if format == domain.FormatSummary {
		promptText = h.Config.DefaultPrompt
	}
	if req.Prompt != "" {
		if len(req.Prompt) > 20000 {
			writeError(w, http.StatusBadRequest, "invalid_request", "prompt too long (max 20000 chars)")
			return
		}
		promptText = req.Prompt
	}

	// Extract YouTube video ID early for dedup and early storage
	var videoID string
	if inputType == domain.InputTypeYouTube {
		var err error
		videoID, err = youtube.ExtractVideoID(req.URL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "could not extract video ID from URL")
			return
		}
	}

	// Dedup: skip cache for custom prompts and text inputs
	isCustomPrompt := req.Prompt != "" && req.Prompt != h.Config.DefaultPrompt
	if inputType == domain.InputTypeYouTube && !isCustomPrompt && videoID != "" {
		// Check for a successful cached run (within TTL)
		since := time.Now().UTC().Add(-h.Config.CacheTTL)
		cachedRun, err := h.Store.FindCachedRun(videoID, format, engine, model, since)
		if err != nil {
			slog.Error("Cache lookup failed", "error", err, "video_id", videoID)
			// Fall through — cache miss is non-fatal
		} else if cachedRun != nil {
			slog.Info("Cache hit", "video_id", videoID, "cached_run_id", cachedRun.ID)
			writeJSON(w, http.StatusOK, CreateSummaryResponse{
				RunID:     cachedRun.ID,
				Status:    cachedRun.Status,
				StatusURL: "/v1/runs/" + cachedRun.ID + "/status",
				ResultURL: "/v1/summaries/" + cachedRun.ID,
				Cached:    true,
			})
			return
		}

		// Check for an in-flight run (queued or running) to avoid duplicate work
		inFlightRun, err := h.Store.FindInFlightRun(videoID, format, engine, model)
		if err != nil {
			slog.Error("In-flight lookup failed", "error", err, "video_id", videoID)
		} else if inFlightRun != nil {
			slog.Info("In-flight dedup", "video_id", videoID, "existing_run_id", inFlightRun.ID, "status", inFlightRun.Status)
			writeJSON(w, http.StatusAccepted, CreateSummaryResponse{
				RunID:     inFlightRun.ID,
				Status:    inFlightRun.Status,
				StatusURL: "/v1/runs/" + inFlightRun.ID + "/status",
				ResultURL: "/v1/summaries/" + inFlightRun.ID,
				Cached:    true,
			})
			return
		}
	}

	// Create run
	runID := uuid.NewString()
	now := time.Now().UTC()

	run := &domain.Run{
		ID:             runID,
		Status:         domain.StatusQueued,
		Stage:          domain.StageQueued,
		InputType:      inputType,
		SourceURL:      req.URL,
		InputText:      req.Text,
		YouTubeVideoID: videoID, // populated early so dedup index works
		Engine:         engine,
		Model:          model,
		Prompt:         promptText,
		Format:         format,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := h.Store.CreateRun(run); err != nil {
		slog.Error("Failed to create run", "error", err)
		writeError(w, http.StatusInternalServerError, "server_error", "failed to create run")
		return
	}

	// Publish NATS event
	evt, err := h.Publisher.PublishSummaryRequested(runID)
	if err != nil {
		slog.Error("Failed to publish event", "error", err, "run_id", runID)
		_ = h.Store.SaveFailed(runID, "event_publish_failed", err.Error(), "", "")
		writeError(w, http.StatusInternalServerError, "server_error", "failed to publish event")
		return
	}

	// Update event published timestamp
	_ = h.Store.UpdateEventPublished(runID, evt.EventID, evt.CreatedAt)

	// Return 202 Accepted
	writeJSON(w, http.StatusAccepted, CreateSummaryResponse{
		RunID:     runID,
		Status:    domain.StatusQueued,
		StatusURL: "/v1/runs/" + runID + "/status",
		ResultURL: "/v1/summaries/" + runID,
	})
}

func (h *Handlers) resolveModel(ctx context.Context, engineName, requested string) (string, int, error) {
	model := requested
	if model == "" {
		model = h.defaultModel(engineName)
	}
	if len(model) > 255 {
		return "", http.StatusBadRequest, fmt.Errorf("model too long (max 255 chars)")
	}
	for _, r := range model {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return "", http.StatusBadRequest, fmt.Errorf("model must not contain newlines or control characters")
		}
	}
	if model == "" {
		return "", 0, nil
	}
	defaulted := requested == ""
	if h.Models == nil {
		if defaulted {
			return model, 0, nil
		}
		return "", http.StatusServiceUnavailable, fmt.Errorf("model catalog unavailable")
	}
	models, err := h.Models.Get(ctx, engineName)
	if err != nil || models.Error != "" {
		if defaulted {
			return model, 0, nil
		}
		return "", http.StatusServiceUnavailable, fmt.Errorf("model catalog unavailable for %s", engineName)
	}
	if len(models.Models) == 0 {
		if defaulted {
			return model, 0, nil
		}
		return "", http.StatusServiceUnavailable, fmt.Errorf("model catalog unavailable for %s", engineName)
	}
	for _, available := range models.Models {
		if available == model {
			return model, 0, nil
		}
	}
	if requested == "" {
		return model, 0, nil
	}
	return "", http.StatusBadRequest, fmt.Errorf("model %q is not available for %s", model, engineName)
}

func (h *Handlers) defaultModel(engineName string) string {
	switch engineName {
	case domain.EnginePi:
		return h.Config.PiModel
	case domain.EngineAgy:
		return h.Config.AgyModel
	default:
		return ""
	}
}

// GetRunStatus handles GET /v1/runs/{run_id}/status.
func (h *Handlers) GetRunStatus(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "run_id is required")
		return
	}

	run, err := h.Store.GetRun(runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "run not found")
		return
	}

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

	run, err := h.Store.GetRun(runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "run not found")
		return
	}

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

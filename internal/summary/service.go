package summary

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/engine"
	"github.com/wayanjimmy/summarize/internal/events"
	"github.com/wayanjimmy/summarize/internal/youtube"
)

// RunStore is the subset of store operations the service needs.
type RunStore interface {
	FindOrCreateRun(run *domain.Run, cacheSince time.Time) (*domain.Run, bool, error)
	GetRunForOwner(id, ownerID string) (*domain.Run, error)
	SaveFailed(runID, errorCode, errorMessage, stderr, command string) error
	UpdateEventPublished(id, eventID string, publishedAt time.Time) error
	CancelRun(id string) error
	UpdatePrompt(id, prompt string) error
}

// EventPublisher publishes summary request events.
type EventPublisher interface {
	PublishSummaryRequested(runID string) (*events.SummaryRequested, error)
}

// WorkflowCanceller cancels a running workflow instance by its stored IDs.
type WorkflowCanceller interface {
	Cancel(ctx context.Context, instanceID, executionID string) error
}

// ServiceConfig holds the configuration values the service needs.
type ServiceConfig struct {
	DefaultEngine string
	DefaultPrompt string
	PiModel       string
	AgyModel      string
	CacheTTL      time.Duration
}

// Service is the protocol-neutral application service for summarization.
type Service struct {
	store     RunStore
	publisher EventPublisher
	models    *engine.ModelCatalog
	config    ServiceConfig
	canceller WorkflowCanceller // optional; nil if cancellation not supported
}

// NewService creates a new summary service.
func NewService(store RunStore, publisher EventPublisher, models *engine.ModelCatalog, cfg ServiceConfig) *Service {
	return &Service{
		store:     store,
		publisher: publisher,
		models:    models,
		config:    cfg,
	}
}

// SetCanceller sets the workflow canceller used by Cancel.
func (s *Service) SetCanceller(c WorkflowCanceller) {
	s.canceller = c
}

// Submit validates the request, handles dedup, creates a run, and publishes an event.
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (*SubmitResult, error) {
	// Validate: exactly one of url or text
	if (req.URL == "" && req.Text == "") || (req.URL != "" && req.Text != "") {
		return nil, invalidInput("exactly one of url or text is required")
	}

	// Determine input type
	var inputType string
	if req.URL != "" {
		if !youtube.IsValidURL(req.URL) {
			return nil, invalidInput("url must be a valid YouTube URL")
		}
		inputType = domain.InputTypeYouTube
	} else {
		if strings.TrimSpace(req.Text) == "" {
			return nil, invalidInput("text must be non-empty")
		}
		inputType = domain.InputTypeText
	}

	// Validate format
	format := req.Format
	if format == "" {
		format = domain.FormatSummary
	}
	if !domain.ValidFormats[format] {
		return nil, invalidInput("format must be one of: summary, chapters, thread, blog")
	}

	// Validate format+input compatibility
	if format == domain.FormatChapters && inputType == domain.InputTypeText {
		return nil, invalidInput("format 'chapters' requires a YouTube URL")
	}

	// Resolve engine
	engineName := s.config.DefaultEngine
	if req.Engine != "" {
		if req.Engine != domain.EnginePi && req.Engine != domain.EngineAgy {
			return nil, invalidInput("engine must be 'pi' or 'agy'")
		}
		engineName = req.Engine
	}

	model, err := s.resolveModel(ctx, engineName, req.Model)
	if err != nil {
		return nil, err
	}

	// Resolve prompt
	promptText := ""
	if format == domain.FormatSummary {
		promptText = s.config.DefaultPrompt
	}
	if req.Prompt != "" {
		if len(req.Prompt) > 20000 {
			return nil, invalidInput("prompt too long (max 20000 chars)")
		}
		promptText = req.Prompt
	}

	// Extract YouTube video ID early for dedup and early storage
	var videoID string
	if inputType == domain.InputTypeYouTube {
		videoID, err = youtube.ExtractVideoID(req.URL)
		if err != nil {
			return nil, invalidInput("could not extract video ID from URL")
		}
	}

	// Build the run to create (or match against for dedup)
	runID := uuid.NewString()
	now := time.Now().UTC()

	run := &domain.Run{
		ID:              runID,
		Status:          domain.StatusQueued,
		Stage:           domain.StageQueued,
		InputType:       inputType,
		SourceURL:       req.URL,
		InputText:       req.Text,
		YouTubeVideoID:  videoID,
		Engine:          engineName,
		Model:           model,
		Prompt:          promptText,
		Format:          format,
		OwnerID:         req.OwnerID,
		IdempotencyKey:  req.IdempotencyKey,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// Determine whether cache/dedup applies (YouTube without custom prompt).
	isCustomPrompt := req.Prompt != "" && req.Prompt != s.config.DefaultPrompt
	var cacheSince time.Time
	if inputType == domain.InputTypeYouTube && !isCustomPrompt && videoID != "" {
		cacheSince = now.Add(-s.config.CacheTTL)
	}

	// Atomic find-or-create: idempotency → cache → in-flight → create.
	existing, cached, err := s.store.FindOrCreateRun(run, cacheSince)
	if err != nil {
		slog.Error("Failed to find-or-create run", "error", err)
		return nil, internalErr("failed to create run")
	}

	if cached {
		slog.Info("Dedup hit", "existing_run_id", existing.ID, "status", existing.Status)
		return &SubmitResult{
			RunID:  existing.ID,
			Status: existing.Status,
			Cached: true,
		}, nil
	}

	// Publish NATS event (only for newly created runs)
	evt, err := s.publisher.PublishSummaryRequested(runID)
	if err != nil {
		slog.Error("Failed to publish event", "error", err, "run_id", runID)
		_ = s.store.SaveFailed(runID, "event_publish_failed", err.Error(), "", "")
		return nil, internalErr("failed to publish event")
	}

	// Update event published timestamp
	_ = s.store.UpdateEventPublished(runID, evt.EventID, evt.CreatedAt)

	return &SubmitResult{
		RunID:  runID,
		Status: domain.StatusQueued,
		Cached: false,
	}, nil
}

// Get retrieves a run by ID, scoped to the given owner.
// Returns a not-found error if the run does not exist or belongs to a different owner.
func (s *Service) Get(ctx context.Context, runID, ownerID string) (*domain.Run, error) {
	run, err := s.store.GetRunForOwner(runID, ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("run not found")
		}
		return nil, internalErr("failed to get run")
	}
	return run, nil
}

// GetTask retrieves a task projection by run ID, scoped to the given owner.
// Returns a not-found error if the run does not exist or belongs to a different owner.
func (s *Service) GetTask(ctx context.Context, runID, ownerID string) (*Task, error) {
	run, err := s.store.GetRunForOwner(runID, ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("run not found")
		}
		return nil, internalErr("failed to get run")
	}
	task := taskFromRun(run)
	return &task, nil
}

// Cancel cancels a run that is in a queued or running state.
// The run is marked as cancelled immediately so subsequent Get calls see the
// cancelled state. If a workflow instance exists and a canceller is configured,
// the workflow is also cancelled to trigger cleanup of any pending activities.
// Returns the updated task state.
func (s *Service) Cancel(ctx context.Context, runID, ownerID string) (*Task, error) {
	run, err := s.store.GetRunForOwner(runID, ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("run not found")
		}
		return nil, internalErr("failed to get run")
	}

	if !domain.IsCancellableStatus(run.Status) {
		return nil, invalidInput(fmt.Sprintf("run cannot be cancelled in status %q", run.Status))
	}

	// Mark the run as cancelled immediately so the state change is visible
	// right away, even before the workflow processes the cancellation event.
	if err := s.store.CancelRun(runID); err != nil {
		slog.Error("Failed to cancel run", "run_id", runID, "error", err)
		return nil, internalErr("failed to cancel run")
	}

	// Cancel the workflow instance if one exists and a canceller is configured.
	// The workflow's deferred cleanup will call CancelRunActivity as a fallback.
	if run.WorkflowInstanceID != "" && s.canceller != nil {
		if err := s.canceller.Cancel(ctx, run.WorkflowInstanceID, run.WorkflowExecutionID); err != nil {
			slog.Warn("Failed to cancel workflow instance",
				"run_id", runID,
				"instance_id", run.WorkflowInstanceID,
				"error", err,
			)
			// Don't fail the cancellation — the run is already marked cancelled.
		}
	}

	// Re-fetch the updated run to return the current state.
	run, err = s.store.GetRunForOwner(runID, ownerID)
	if err != nil {
		return nil, internalErr("failed to get updated run")
	}
	task := taskFromRun(run)
	return &task, nil
}

// Update modifies a queued run's prompt before the workflow picks it up.
// Only runs in the queued status can be updated. Returns the updated task state.
func (s *Service) Update(ctx context.Context, req UpdateRequest) (*Task, error) {
	run, err := s.store.GetRunForOwner(req.RunID, req.OwnerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("run not found")
		}
		return nil, internalErr("failed to get run")
	}

	if run.Status != domain.StatusQueued {
		return nil, invalidInput(fmt.Sprintf("run can only be updated while queued, current status: %q", run.Status))
	}

	if req.Prompt != "" {
		if len(req.Prompt) > 20000 {
			return nil, invalidInput("prompt too long (max 20000 chars)")
		}
		if err := s.store.UpdatePrompt(req.RunID, req.Prompt); err != nil {
			slog.Error("Failed to update prompt", "run_id", req.RunID, "error", err)
			return nil, internalErr("failed to update run")
		}
	}

	// Re-fetch the updated run.
	run, err = s.store.GetRunForOwner(req.RunID, req.OwnerID)
	if err != nil {
		return nil, internalErr("failed to get updated run")
	}
	task := taskFromRun(run)
	return &task, nil
}

// ListModels returns model availability for all engines.
func (s *Service) ListModels(ctx context.Context) (*ListModelsResult, error) {
	if s.models == nil {
		return nil, serviceUnavailable("model catalog unavailable")
	}

	listed := s.models.ListAll(ctx)
	result := &ListModelsResult{
		Engines: map[string]EngineModelInfo{},
	}

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
		result.Engines[name] = EngineModelInfo{
			Models:       models.Models,
			DefaultModel: s.defaultModel(name),
			Status:       status,
			Available:    available,
			Stale:        models.Stale,
			FetchedAt:    models.FetchedAt,
			Error:        models.Error,
		}
	}

	return result, nil
}

func (s *Service) resolveModel(ctx context.Context, engineName, requested string) (string, error) {
	model := requested
	if model == "" {
		model = s.defaultModel(engineName)
	}
	if len(model) > 255 {
		return "", invalidInput("model too long (max 255 chars)")
	}
	for _, r := range model {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return "", invalidInput("model must not contain newlines or control characters")
		}
	}
	if model == "" {
		return "", nil
	}
	defaulted := requested == ""
	if s.models == nil {
		if defaulted {
			return model, nil
		}
		return "", serviceUnavailable("model catalog unavailable")
	}
	models, err := s.models.Get(ctx, engineName)
	if err != nil || models.Error != "" {
		if defaulted {
			return model, nil
		}
		return "", serviceUnavailable(fmt.Sprintf("model catalog unavailable for %s", engineName))
	}
	if len(models.Models) == 0 {
		if defaulted {
			return model, nil
		}
		return "", serviceUnavailable(fmt.Sprintf("model catalog unavailable for %s", engineName))
	}
	for _, available := range models.Models {
		if available == model {
			return model, nil
		}
	}
	if requested == "" {
		return model, nil
	}
	return "", invalidInput(fmt.Sprintf("model %q is not available for %s", model, engineName))
}

func (s *Service) defaultModel(engineName string) string {
	switch engineName {
	case domain.EnginePi:
		return s.config.PiModel
	case domain.EngineAgy:
		return s.config.AgyModel
	default:
		return ""
	}
}

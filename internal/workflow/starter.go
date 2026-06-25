package workflow

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cschleiden/go-workflows/client"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/events"
)

// Starter listens for NATS events and starts go-workflows instances.
type Starter struct {
	wfClient *client.Client
	log      *slog.Logger
}

// NewStarter creates a new workflow Starter.
func NewStarter(wfClient *client.Client) *Starter {
	return &Starter{
		wfClient: wfClient,
		log:      slog.With("component", "workflow.starter"),
	}
}

// HandleEvent is the NATS handler that starts a workflow for a summary request.
func (s *Starter) HandleEvent(ctx context.Context) events.Handler {
	return func(evt *events.SummaryRequested) {
		s.log.Info("Received summary.requested",
			"event_id", evt.EventID,
			"run_id", evt.RunID,
		)

		// Check if run already has a workflow (idempotency)
		run, err := deps.Store.GetRun(evt.RunID)
		if err != nil {
			s.log.Error("Failed to load run", "run_id", evt.RunID, "error", err)
			return
		}

		if run.WorkflowInstanceID != "" {
			s.log.Info("Workflow already started, skipping", "run_id", evt.RunID)
			return
		}

		if run.Status != "queued" {
			s.log.Info("Run not in queued state, skipping",
				"run_id", evt.RunID,
				"status", run.Status,
			)
			return
		}

		// Start workflow
		instanceID := makeInstanceID(run)
		wf, err := s.wfClient.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
			InstanceID: instanceID,
		}, SummaryWorkflow, SummaryWorkflowInput{
			RunID: evt.RunID,
		})
		if err != nil {
			s.log.Error("Failed to start workflow", "run_id", evt.RunID, "error", err)
			_ = deps.Store.SaveFailed(evt.RunID, "workflow_start_failed", err.Error(), "", "")
			return
		}

		// Store workflow IDs
		err = deps.Store.UpdateWorkflowIDs(evt.RunID, wf.InstanceID, wf.ExecutionID)
		if err != nil {
			s.log.Error("Failed to store workflow IDs", "run_id", evt.RunID, "error", err)
		}

		s.log.Info("Workflow started",
			"run_id", evt.RunID,
			"instance_id", wf.InstanceID,
			"execution_id", wf.ExecutionID,
			"instance_id_display", instanceID,
		)
	}
}

func RecoverQueuedRuns(ctx context.Context, pub *events.Publisher) error {
	runs, err := deps.Store.FindQueuedRunsWithoutWorkflow()
	if err != nil {
		slog.Error("Failed to find queued runs", "error", err)
		return err
	}

	if len(runs) > 0 {
		slog.Info("Recovering queued runs without workflow", "count", len(runs))
	}

	for _, run := range runs {
		evt, err := pub.PublishSummaryRequested(run.ID)
		if err != nil {
			slog.Error("Failed to republish event", "run_id", run.ID, "error", err)
			continue
		}
		_ = deps.Store.UpdateEventPublished(run.ID, evt.EventID, evt.CreatedAt)
		slog.Info("Republished event for queued run", "run_id", run.ID, "event_id", evt.EventID)
	}

	return nil
}

// shortType returns a concise label for the input type for use in instance IDs.
func shortType(t string) string {
	switch t {
	case domain.InputTypeYouTube:
		return "yt"
	case domain.InputTypeText:
		return "txt"
	default:
		return t
	}
}

// makeInstanceID builds a descriptive workflow instance ID from the run,
// so the go-workflows diagnostics dashboard shows key info at a glance.
// Format: summary:<input_type>:<engine>:<short_run_id>
func makeInstanceID(run *domain.Run) string {
	inputType := shortType(run.InputType)
	engine := run.Engine
	if engine == "" {
		engine = "?"
	}
	shortID := run.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("summary:%s:%s:%s", inputType, engine, shortID)
}

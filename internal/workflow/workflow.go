package workflow

import (
	"strings"
	"time"

	"github.com/cschleiden/go-workflows/workflow"
)

// SummaryWorkflowInput is the input for the summary workflow.
type SummaryWorkflowInput struct {
	RunID string
}

// SummaryWorkflow is a batch workflow that summarizes content.
// Activities read/write state from the app DB by run_id.
func SummaryWorkflow(ctx workflow.Context, input SummaryWorkflowInput) error {
	logger := workflow.Logger(ctx)
	logger.Info("Starting summary workflow", "run_id", input.RunID)

	// Mark as running
	_, err := workflow.ExecuteActivity[any](ctx, workflow.DefaultActivityOptions, MarkRunningActivity, input.RunID).Get(ctx)
	if err != nil {
		logger.Error("Failed to mark running", "error", err)
		return err
	}

	// Load run to determine input type
	runMeta, err := workflow.ExecuteActivity[*RunMeta](ctx, workflow.DefaultActivityOptions, LoadRunActivity, input.RunID).Get(ctx)
	if err != nil {
		logger.Error("Failed to load run", "error", err)
		_, _ = workflow.ExecuteActivity[any](ctx, workflow.DefaultActivityOptions, FailRunActivity, input.RunID, "workflow_failed", "failed to load run: "+err.Error()).Get(ctx)
		return err
	}

	// Fetch transcript if YouTube
	if runMeta.InputType == "youtube" {
		transcriptOpts := workflow.ActivityOptions{
			RetryOptions: workflow.RetryOptions{
				MaxAttempts:        2,
				FirstRetryInterval: 2 * time.Second,
			},
		}
		_, err = workflow.ExecuteActivity[any](ctx, transcriptOpts, FetchTranscriptActivity, input.RunID).Get(ctx)
		if err != nil {
			logger.Error("Failed to fetch transcript", "error", err)
			// Extract error code from error message (format: "code: message")
			// Default to transcript_unavailable if no code prefix found
			errorCode := "transcript_unavailable"
			errMsg := err.Error()
			if idx := strings.Index(errMsg, ": "); idx > 0 && idx < 40 {
				potentialCode := errMsg[:idx]
				// Only use it if it looks like an error code (lowercase_alphanumeric)
				if strings.Contains(potentialCode, "_") && !strings.Contains(potentialCode, " ") {
					errorCode = potentialCode
					errMsg = errMsg[idx+2:]
				}
			}
			_, _ = workflow.ExecuteActivity[any](ctx, workflow.DefaultActivityOptions, FailRunActivity, input.RunID, errorCode, errMsg).Get(ctx)
			return nil // workflow completes gracefully on failure
		}
	}

	// Summarize — MaxAttempts 1 because CLI spawns are not idempotent
	summarizeOpts := workflow.ActivityOptions{
		RetryOptions: workflow.RetryOptions{
			MaxAttempts:  1,
			RetryTimeout: 30 * time.Minute,
		},
	}
	_, err = workflow.ExecuteActivity[any](ctx, summarizeOpts, SummarizeActivity, input.RunID).Get(ctx)
	if err != nil {
		logger.Error("Failed to summarize", "error", err)
		_, _ = workflow.ExecuteActivity[any](ctx, workflow.DefaultActivityOptions, FailRunActivity, input.RunID, "engine_failed", err.Error()).Get(ctx)
		return nil
	}

	logger.Info("Summary workflow completed", "run_id", input.RunID)
	return nil
}

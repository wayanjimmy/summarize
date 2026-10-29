package workflow_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/worker"
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/engine"
	"github.com/wayanjimmy/summarize/internal/store"
	summaryworkflow "github.com/wayanjimmy/summarize/internal/workflow"
)

// Only the external LLM is substituted; workers, activities and both SQLite
// databases are real. The result must identify the persisted run, not the
// workflow instance, and remain useful when an application failure is handled.
func TestSummaryWorkflowResultReferencesStoredRun(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "engine failure"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := store.New(filepath.Join(dir, "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			runID := "b4203f9d-d8ba-4a17-b922-98442950f240"
			if err := s.CreateRun(&domain.Run{
				ID: runID, Status: domain.StatusQueued, Stage: domain.StageQueued,
				InputType: domain.InputTypeText, InputText: "A short source document.",
				Engine: domain.EnginePi, Format: domain.FormatSummary,
			}); err != nil {
				t.Fatal(err)
			}
			summaryworkflow.SetDeps(summaryworkflow.Deps{
				Store: s, Config: &config.Config{DataDir: dir, MaxInputChars: 10000, RunTimeout: time.Second},
				PiEngine: &fixtureEngine{failed: failed},
			})
			defer summaryworkflow.SetDeps(summaryworkflow.Deps{})
			b := sqlite.NewSqliteBackend(filepath.Join(dir, "workflows.db"))
			defer b.Close()
			w := worker.New(b, nil)
			if err := w.RegisterWorkflow(summaryworkflow.SummaryWorkflow); err != nil {
				t.Fatal(err)
			}
			for _, activity := range []any{
				summaryworkflow.MarkRunningActivity, summaryworkflow.LoadRunActivity,
				summaryworkflow.SummarizeActivity, summaryworkflow.FailRunActivity,
			} {
				if err := w.RegisterActivity(activity); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := w.Start(ctx); err != nil {
				cancel()
				t.Fatal(err)
			}
			defer func() { cancel(); _ = w.WaitForCompletion() }()
			c := client.New(b)
			instance, err := c.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
				InstanceID: "summary:txt:pi:b4203f9d",
			}, summaryworkflow.SummaryWorkflow, summaryworkflow.SummaryWorkflowInput{RunID: runID})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.GetWorkflowResult[map[string]string](ctx, c, instance, 15*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if len(result) != 1 || result["run_id"] != runID {
				t.Fatalf("result = %#v, want only run_id %q", result, runID)
			}
			run, err := s.GetRun(result["run_id"])
			if err != nil {
				t.Fatal(err)
			}
			if failed {
				if run.Status != domain.StatusFailed || run.ErrorCode != "engine_failed" {
					t.Fatalf("referenced run = %s/%s, want failed/engine_failed", run.Status, run.ErrorCode)
				}
			} else if run.Status != domain.StatusSucceeded || run.Summary != "A concise summary." {
				t.Fatalf("referenced run = %s/%q, want succeeded with stored summary", run.Status, run.Summary)
			}
		})
	}
}

type fixtureEngine struct{ failed bool }

func (*fixtureEngine) Name() string { return domain.EnginePi }

func (e *fixtureEngine) Run(context.Context, engine.Request) (engine.Result, error) {
	if e.failed {
		return engine.Result{}, errors.New("fixture engine unavailable")
	}
	return engine.Result{Text: "A concise summary."}, nil
}

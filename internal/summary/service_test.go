package summary

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/engine"
	"github.com/wayanjimmy/summarize/internal/events"
	"github.com/wayanjimmy/summarize/internal/store"
)

// --- test helpers ---

type testLister struct {
	name   string
	models []string
	err    error
}

func (l *testLister) Name() string { return l.name }

func (l *testLister) ListModels(context.Context) ([]string, error) {
	return l.models, l.err
}

type mockPublisher struct {
	err error
}

func (m *mockPublisher) PublishSummaryRequested(runID string) (*events.SummaryRequested, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &events.SummaryRequested{
		EventID:   uuid.NewString(),
		EventType: "summary.requested",
		RunID:     runID,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func isCategory(err error, cat ErrorCategory) bool {
	var se *Error
	if errors.As(err, &se) {
		return se.Category == cat
	}
	return false
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- resolveModel tests ---

func TestResolveModel(t *testing.T) {
	s := NewService(nil, nil,
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"default", "ok"}}),
		ServiceConfig{PiModel: "default"},
	)

	// Valid explicit model
	got, err := s.resolveModel(context.Background(), "pi", "ok")
	if err != nil || got != "ok" {
		t.Fatalf("valid model = %q, %v", got, err)
	}

	// Invalid explicit model → invalid_input
	_, err = s.resolveModel(context.Background(), "pi", "bad")
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("invalid model: expected invalid_input, got %v", err)
	}

	// Offline catalog + explicit model → service_unavailable
	s.models = engine.NewModelCatalog(0, &testLister{name: "pi", err: errors.New("offline")})
	_, err = s.resolveModel(context.Background(), "pi", "ok")
	if !isCategory(err, CategoryServiceUnavailable) {
		t.Fatalf("offline explicit: expected service_unavailable, got %v", err)
	}

	// Offline catalog + default model → fallback OK
	got, err = s.resolveModel(context.Background(), "pi", "")
	if err != nil || got != "default" {
		t.Fatalf("offline default = %q, %v", got, err)
	}

	// Empty catalog + default model → fallback OK
	s.models = engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{}})
	got, err = s.resolveModel(context.Background(), "pi", "")
	if err != nil || got != "default" {
		t.Fatalf("empty default = %q, %v", got, err)
	}

	// Empty catalog + explicit model → service_unavailable
	_, err = s.resolveModel(context.Background(), "pi", "ok")
	if !isCategory(err, CategoryServiceUnavailable) {
		t.Fatalf("empty explicit: expected service_unavailable, got %v", err)
	}
}

// --- Submit validation tests ---

func TestSubmit_ExactlyOneOfURLorText(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{})

	_, err := s.Submit(context.Background(), SubmitRequest{})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("both empty: expected invalid_input, got %v", err)
	}

	_, err = s.Submit(context.Background(), SubmitRequest{
		URL:  "https://youtube.com/watch?v=abc",
		Text: "hello",
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("both non-empty: expected invalid_input, got %v", err)
	}
}

func TestSubmit_InvalidYouTubeURL(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{})
	_, err := s.Submit(context.Background(), SubmitRequest{
		URL: "https://example.com/watch?v=abc",
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
}

func TestSubmit_EmptyText(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{})
	_, err := s.Submit(context.Background(), SubmitRequest{
		Text: "   ",
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
}

func TestSubmit_InvalidFormat(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{})
	_, err := s.Submit(context.Background(), SubmitRequest{
		Text:   "hello",
		Format: "invalid",
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
	if !strings.Contains(err.Error(), "format must be one of") {
		t.Errorf("error = %q, want format validation", err.Error())
	}
}

func TestSubmit_ChaptersWithTextRejected(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{})
	_, err := s.Submit(context.Background(), SubmitRequest{
		Text:   "some long text",
		Format: "chapters",
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
	if !strings.Contains(err.Error(), "chapters") {
		t.Errorf("error = %q, want chapters error", err.Error())
	}
}

func TestSubmit_InvalidEngine(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{DefaultEngine: "pi"})
	_, err := s.Submit(context.Background(), SubmitRequest{
		Text:   "hello",
		Engine: "invalid",
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
}

func TestSubmit_PromptTooLong(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{
		DefaultEngine: "pi",
		DefaultPrompt: config.DefaultPrompt,
	})
	_, err := s.Submit(context.Background(), SubmitRequest{
		Text:   "hello",
		Prompt: strings.Repeat("a", 20001),
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input, got %v", err)
	}
}

// --- Submit dedup tests ---

func TestSubmit_CacheHit(t *testing.T) {
	st := newTestStore(t)

	st.CreateRun(&domain.Run{
		ID: "cached-run-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	s := NewService(st, nil,
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		ServiceConfig{
			DefaultEngine: "pi",
			DefaultPrompt: config.DefaultPrompt,
			PiModel:       "pi-model",
			CacheTTL:      7 * 24 * time.Hour,
		},
	)

	result, err := s.Submit(context.Background(), SubmitRequest{
		URL: "https://youtube.com/watch?v=abc123",
	})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if result.RunID != "cached-run-1" {
		t.Errorf("RunID = %q, want %q", result.RunID, "cached-run-1")
	}
	if !result.Cached {
		t.Error("Cached = false, want true")
	}
	if result.Status != domain.StatusSucceeded {
		t.Errorf("Status = %q, want %q", result.Status, domain.StatusSucceeded)
	}
}

func TestSubmit_InFlightDedup(t *testing.T) {
	st := newTestStore(t)

	st.CreateRun(&domain.Run{
		ID: "inflight-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=xyz789",
		YouTubeVideoID: "xyz789", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	s := NewService(st, nil,
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		ServiceConfig{
			DefaultEngine: "pi",
			DefaultPrompt: config.DefaultPrompt,
			PiModel:       "pi-model",
			CacheTTL:      7 * 24 * time.Hour,
		},
	)

	result, err := s.Submit(context.Background(), SubmitRequest{
		URL: "https://youtu.be/xyz789",
	})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if result.RunID != "inflight-1" {
		t.Errorf("RunID = %q, want %q", result.RunID, "inflight-1")
	}
	if !result.Cached {
		t.Error("Cached = false, want true")
	}
}

func TestSubmit_CustomPromptBypassesCache(t *testing.T) {
	st := newTestStore(t)

	st.CreateRun(&domain.Run{
		ID: "cached-default", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	s := NewService(st, &mockPublisher{},
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		ServiceConfig{
			DefaultEngine: "pi",
			DefaultPrompt: config.DefaultPrompt,
			PiModel:       "pi-model",
			CacheTTL:      7 * 24 * time.Hour,
		},
	)

	result, err := s.Submit(context.Background(), SubmitRequest{
		URL:    "https://youtube.com/watch?v=abc123",
		Prompt: "Custom special prompt",
	})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if result.Cached {
		t.Fatal("custom prompt should bypass cache, but got cache hit")
	}
	if result.RunID == "cached-default" {
		t.Fatal("should not return cached run ID")
	}
}

func TestSubmit_NewRun(t *testing.T) {
	st := newTestStore(t)

	s := NewService(st, &mockPublisher{},
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		ServiceConfig{
			DefaultEngine: "pi",
			DefaultPrompt: config.DefaultPrompt,
			PiModel:       "pi-model",
			CacheTTL:      7 * 24 * time.Hour,
		},
	)

	result, err := s.Submit(context.Background(), SubmitRequest{
		Text: "some text to summarize",
	})
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if result.Cached {
		t.Error("Cached = true, want false")
	}
	if result.Status != domain.StatusQueued {
		t.Errorf("Status = %q, want %q", result.Status, domain.StatusQueued)
	}
	if result.RunID == "" {
		t.Error("RunID = empty, want non-empty")
	}

	// Verify run was persisted
	run, err := st.GetRun(result.RunID)
	if err != nil {
		t.Fatalf("GetRun error: %v", err)
	}
	if run.Status != domain.StatusQueued {
		t.Errorf("run status = %q, want %q", run.Status, domain.StatusQueued)
	}
}

func TestSubmit_PublishFailure(t *testing.T) {
	st := newTestStore(t)

	s := NewService(st, &mockPublisher{err: errors.New("nats down")},
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		ServiceConfig{
			DefaultEngine: "pi",
			DefaultPrompt: config.DefaultPrompt,
			PiModel:       "pi-model",
			CacheTTL:      7 * 24 * time.Hour,
		},
	)

	_, err := s.Submit(context.Background(), SubmitRequest{
		Text: "some text",
	})
	if !isCategory(err, CategoryInternal) {
		t.Fatalf("expected internal, got %v", err)
	}
}

// --- Get tests ---

func TestGet_NotFound(t *testing.T) {
	st := newTestStore(t)
	s := NewService(st, nil, nil, ServiceConfig{})

	_, err := s.Get(context.Background(), "nonexistent", "local")
	if !isCategory(err, CategoryNotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestGet_Success(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "run-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test prompt",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	run, err := s.Get(context.Background(), "run-1", "local")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if run.ID != "run-1" {
		t.Errorf("ID = %q, want %q", run.ID, "run-1")
	}
}

// --- ListModels tests ---

func TestListModels_PartialFailure(t *testing.T) {
	s := NewService(nil, nil,
		engine.NewModelCatalog(0,
			&testLister{name: "pi", models: []string{"pi-model"}},
			&testLister{name: "agy", err: errors.New("offline")},
		),
		ServiceConfig{},
	)

	result, err := s.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}

	piInfo := result.Engines["pi"]
	if len(piInfo.Models) != 1 || piInfo.Models[0] != "pi-model" {
		t.Errorf("pi models = %v, want [pi-model]", piInfo.Models)
	}
	if piInfo.Status != "available" {
		t.Errorf("pi status = %q, want available", piInfo.Status)
	}

	agyInfo := result.Engines["agy"]
	if agyInfo.Status != "unavailable" {
		t.Errorf("agy status = %q, want unavailable", agyInfo.Status)
	}
}

func TestListModels_NilCatalog(t *testing.T) {
	s := NewService(nil, nil, nil, ServiceConfig{})
	_, err := s.ListModels(context.Background())
	if !isCategory(err, CategoryServiceUnavailable) {
		t.Fatalf("expected service_unavailable, got %v", err)
	}
}

// --- GetTask tests ---

func TestGetTask_Success(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "task-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test prompt", Summary: "the summary",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	task, err := s.GetTask(context.Background(), "task-1", "local")
	if err != nil {
		t.Fatalf("GetTask error: %v", err)
	}
	if task.ID != "task-1" {
		t.Errorf("ID = %q, want task-1", task.ID)
	}
	if task.Status != domain.StatusSucceeded {
		t.Errorf("Status = %q, want %q", task.Status, domain.StatusSucceeded)
	}
	if task.Summary != "the summary" {
		t.Errorf("Summary = %q, want %q", task.Summary, "the summary")
	}
	if !task.IsTerminal() {
		t.Error("IsTerminal = false, want true for succeeded")
	}
}

func TestGetTask_NotFound(t *testing.T) {
	st := newTestStore(t)
	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.GetTask(context.Background(), "nonexistent", "local")
	if !isCategory(err, CategoryNotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestGetTask_OwnerIsolation(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "alice-run", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", OwnerID: "alice",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.GetTask(context.Background(), "alice-run", "bob")
	if !isCategory(err, CategoryNotFound) {
		t.Fatalf("bob should get not_found for alice's run, got %v", err)
	}

	task, err := s.GetTask(context.Background(), "alice-run", "alice")
	if err != nil {
		t.Fatalf("alice should access her own run: %v", err)
	}
	if task.ID != "alice-run" {
		t.Errorf("ID = %q, want alice-run", task.ID)
	}
}

// --- Cancel tests ---

type mockCanceller struct {
	called      bool
	instanceID  string
	executionID string
	err         error
}

func (m *mockCanceller) Cancel(_ context.Context, instanceID, executionID string) error {
	m.called = true
	m.instanceID = instanceID
	m.executionID = executionID
	return m.err
}

func TestCancel_QueuedRun(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "queued-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	mc := &mockCanceller{}
	s := NewService(st, nil, nil, ServiceConfig{})
	s.SetCanceller(mc)

	task, err := s.Cancel(context.Background(), "queued-1", "local")
	if err != nil {
		t.Fatalf("Cancel error: %v", err)
	}
	if task.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, want %q", task.Status, domain.StatusCancelled)
	}
	if task.Stage != domain.StageCancelled {
		t.Errorf("Stage = %q, want %q", task.Stage, domain.StageCancelled)
	}
	if !task.IsTerminal() {
		t.Error("IsTerminal = false, want true for cancelled")
	}
	if task.IsCancellable() {
		t.Error("IsCancellable = true after cancellation, want false")
	}
	// No workflow instance ID → canceller should not be called
	if mc.called {
		t.Error("canceller should not be called for run without workflow instance")
	}
}

func TestCancel_RunningRunWithWorkflow(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "running-1", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
		WorkflowInstanceID:  "inst-123",
		WorkflowExecutionID: "exec-456",
	})

	mc := &mockCanceller{}
	s := NewService(st, nil, nil, ServiceConfig{})
	s.SetCanceller(mc)

	task, err := s.Cancel(context.Background(), "running-1", "local")
	if err != nil {
		t.Fatalf("Cancel error: %v", err)
	}
	if task.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, want %q", task.Status, domain.StatusCancelled)
	}
	// Cancellers should have been called with the workflow IDs
	if !mc.called {
		t.Fatal("canceller should be called for run with workflow instance")
	}
	if mc.instanceID != "inst-123" {
		t.Errorf("instanceID = %q, want inst-123", mc.instanceID)
	}
	if mc.executionID != "exec-456" {
		t.Errorf("executionID = %q, want exec-456", mc.executionID)
	}
}

func TestCancel_SucceededRunRejected(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "done-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", Summary: "result",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Cancel(context.Background(), "done-1", "local")
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input for succeeded run, got %v", err)
	}

	// Verify run was not modified
	run, _ := st.GetRun("done-1")
	if run.Status != domain.StatusSucceeded {
		t.Errorf("run status changed to %q, should remain succeeded", run.Status)
	}
}

func TestCancel_FailedRunRejected(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "failed-1", Status: domain.StatusFailed, Stage: domain.StageFailed,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", ErrorCode: "engine_failed",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Cancel(context.Background(), "failed-1", "local")
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input for failed run, got %v", err)
	}
}

func TestCancel_AlreadyCancelled(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "cancelled-1", Status: domain.StatusCancelled, Stage: domain.StageCancelled,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Cancel(context.Background(), "cancelled-1", "local")
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input for already cancelled run, got %v", err)
	}
}

func TestCancel_NotFound(t *testing.T) {
	st := newTestStore(t)
	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Cancel(context.Background(), "nonexistent", "local")
	if !isCategory(err, CategoryNotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestCancel_OwnerIsolation(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "alice-run", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", OwnerID: "alice",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Cancel(context.Background(), "alice-run", "bob")
	if !isCategory(err, CategoryNotFound) {
		t.Fatalf("bob should get not_found for alice's run, got %v", err)
	}

	// Verify run was not cancelled
	run, _ := st.GetRun("alice-run")
	if run.Status != domain.StatusQueued {
		t.Errorf("run status changed to %q, should remain queued", run.Status)
	}
}

func TestCancel_CancellerErrorDoesNotFailCancellation(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "run-1", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
		WorkflowInstanceID: "inst-123",
	})

	mc := &mockCanceller{err: errors.New("workflow already completed")}
	s := NewService(st, nil, nil, ServiceConfig{})
	s.SetCanceller(mc)

	task, err := s.Cancel(context.Background(), "run-1", "local")
	if err != nil {
		t.Fatalf("Cancel should succeed even if canceller fails, got %v", err)
	}
	if task.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, want %q", task.Status, domain.StatusCancelled)
	}
}

func TestCancel_NoCancellerConfigured(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "run-1", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
		WorkflowInstanceID: "inst-123",
	})

	// No canceller set — should still mark the run as cancelled
	s := NewService(st, nil, nil, ServiceConfig{})
	task, err := s.Cancel(context.Background(), "run-1", "local")
	if err != nil {
		t.Fatalf("Cancel error: %v", err)
	}
	if task.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, want %q", task.Status, domain.StatusCancelled)
	}
}

// --- Update tests ---

func TestUpdate_QueuedRunPrompt(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "queued-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old prompt",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	task, err := s.Update(context.Background(), UpdateRequest{
		RunID:   "queued-1",
		OwnerID: "local",
		Prompt:  "new prompt",
	})
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if task.Status != domain.StatusQueued {
		t.Errorf("Status = %q, want %q", task.Status, domain.StatusQueued)
	}

	// Verify prompt was persisted
	run, _ := st.GetRun("queued-1")
	if run.Prompt != "new prompt" {
		t.Errorf("Prompt = %q, want %q", run.Prompt, "new prompt")
	}
}

func TestUpdate_RunningRunRejected(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "running-1", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old prompt",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Update(context.Background(), UpdateRequest{
		RunID:   "running-1",
		OwnerID: "local",
		Prompt:  "new prompt",
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input for running run, got %v", err)
	}

	// Verify prompt was not changed
	run, _ := st.GetRun("running-1")
	if run.Prompt != "old prompt" {
		t.Errorf("Prompt = %q, want %q", run.Prompt, "old prompt")
	}
}

func TestUpdate_PromptTooLong(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "queued-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Update(context.Background(), UpdateRequest{
		RunID:   "queued-1",
		OwnerID: "local",
		Prompt:  strings.Repeat("a", 20001),
	})
	if !isCategory(err, CategoryInvalidInput) {
		t.Fatalf("expected invalid_input for too long prompt, got %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	st := newTestStore(t)
	s := NewService(st, nil, nil, ServiceConfig{})
	_, err := s.Update(context.Background(), UpdateRequest{
		RunID:   "nonexistent",
		OwnerID: "local",
		Prompt:  "new",
	})
	if !isCategory(err, CategoryNotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestUpdate_EmptyPromptNoOp(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "queued-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "existing prompt",
	})

	s := NewService(st, nil, nil, ServiceConfig{})
	task, err := s.Update(context.Background(), UpdateRequest{
		RunID:   "queued-1",
		OwnerID: "local",
	})
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if task.Status != domain.StatusQueued {
		t.Errorf("Status = %q, want %q", task.Status, domain.StatusQueued)
	}
	// Prompt should be unchanged
	run, _ := st.GetRun("queued-1")
	if run.Prompt != "existing prompt" {
		t.Errorf("Prompt = %q, want %q", run.Prompt, "existing prompt")
	}
}

// --- Store terminal-state guard tests ---

func TestStore_CancelRun_QueuedToCancelled(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "q1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	if err := st.CancelRun("q1"); err != nil {
		t.Fatalf("CancelRun error: %v", err)
	}
	run, _ := st.GetRun("q1")
	if run.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, want %q", run.Status, domain.StatusCancelled)
	}
	if run.Stage != domain.StageCancelled {
		t.Errorf("Stage = %q, want %q", run.Stage, domain.StageCancelled)
	}
	if run.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after cancellation")
	}
}

func TestStore_CancelRun_TerminalNotOverwritten(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "s1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", Summary: "result",
	})

	if err := st.CancelRun("s1"); err != nil {
		t.Fatalf("CancelRun error: %v", err)
	}
	run, _ := st.GetRun("s1")
	if run.Status != domain.StatusSucceeded {
		t.Errorf("Status = %q, should remain succeeded", run.Status)
	}
}

func TestStore_SaveSucceeded_DoesNotOverwriteCancelled(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "c1", Status: domain.StatusCancelled, Stage: domain.StageCancelled,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	if err := st.SaveSucceeded("c1", "late summary", "", ""); err != nil {
		t.Fatalf("SaveSucceeded error: %v", err)
	}
	run, _ := st.GetRun("c1")
	if run.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, should remain cancelled", run.Status)
	}
	if run.Summary != "" {
		t.Errorf("Summary = %q, should be empty (not overwritten)", run.Summary)
	}
}

func TestStore_SaveFailed_DoesNotOverwriteCancelled(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "c1", Status: domain.StatusCancelled, Stage: domain.StageCancelled,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	if err := st.SaveFailed("c1", "engine_failed", "late error", "", ""); err != nil {
		t.Fatalf("SaveFailed error: %v", err)
	}
	run, _ := st.GetRun("c1")
	if run.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, should remain cancelled", run.Status)
	}
	if run.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, should be empty (not overwritten)", run.ErrorCode)
	}
}

func TestStore_MarkRunning_DoesNotOverwriteCancelled(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "c1", Status: domain.StatusCancelled, Stage: domain.StageCancelled,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	if err := st.MarkRunning("c1"); err != nil {
		t.Fatalf("MarkRunning error: %v", err)
	}
	run, _ := st.GetRun("c1")
	if run.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, should remain cancelled", run.Status)
	}
}

func TestStore_FailRunIfNotTerminal_DoesNotOverwriteCancelled(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "c1", Status: domain.StatusCancelled, Stage: domain.StageCancelled,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	if err := st.FailRunIfNotTerminal("c1", "engine_failed", "late error"); err != nil {
		t.Fatalf("FailRunIfNotTerminal error: %v", err)
	}
	run, _ := st.GetRun("c1")
	if run.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, should remain cancelled", run.Status)
	}
}

func TestStore_UpdatePrompt_OnlyQueued(t *testing.T) {
	st := newTestStore(t)
	st.CreateRun(&domain.Run{
		ID: "r1", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old",
	})

	if err := st.UpdatePrompt("r1", "new"); err != nil {
		t.Fatalf("UpdatePrompt error: %v", err)
	}
	run, _ := st.GetRun("r1")
	if run.Prompt != "old" {
		t.Errorf("Prompt = %q, should remain %q for running run", run.Prompt, "old")
	}
}

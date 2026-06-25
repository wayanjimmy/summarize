package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wayanjimmy/summarize/internal/domain"
	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGetRun(t *testing.T) {
	s := newTestStore(t)

	run := &domain.Run{
		ID:        "test-run-1",
		Status:    domain.StatusQueued,
		Stage:     domain.StageQueued,
		InputType: domain.InputTypeText,
		InputText: "Some test content",
		Engine:    domain.EnginePi,
		Prompt:    "Summarize this",
	}

	if err := s.CreateRun(run); err != nil {
		t.Fatalf("CreateRun() error: %v", err)
	}

	got, err := s.GetRun("test-run-1")
	if err != nil {
		t.Fatalf("GetRun() error: %v", err)
	}

	if got.ID != "test-run-1" {
		t.Errorf("ID = %q, want %q", got.ID, "test-run-1")
	}
	if got.Status != domain.StatusQueued {
		t.Errorf("Status = %q, want %q", got.Status, domain.StatusQueued)
	}
	if got.InputType != domain.InputTypeText {
		t.Errorf("InputType = %q, want %q", got.InputType, domain.InputTypeText)
	}
	if got.InputText != "Some test content" {
		t.Errorf("InputText = %q, want %q", got.InputText, "Some test content")
	}
	if got.Transcript != "" {
		t.Errorf("Transcript = %q, want empty", got.Transcript)
	}
	if got.Summary != "" {
		t.Errorf("Summary = %q, want empty", got.Summary)
	}
}

func TestGetRunHandlesMigratedDBColumnOrder(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	oldSchema := strings.Replace(schemaSQL, "  model TEXT,\n\n", "", 1)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	if _, err := db.Exec(oldSchema); err != nil {
		db.Close()
		t.Fatalf("create old schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close old db: %v", err)
	}

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() migrated old schema error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	run := &domain.Run{
		ID:        "migrated-order-run",
		Status:    domain.StatusQueued,
		Stage:     domain.StageQueued,
		InputType: domain.InputTypeYouTube,
		SourceURL: "https://youtube.com/watch?v=abc",
		Engine:    domain.EnginePi,
		Model:     "test-model",
		Prompt:    "Summarize this",
	}
	if err := s.CreateRun(run); err != nil {
		t.Fatalf("CreateRun() error: %v", err)
	}

	got, err := s.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun() error: %v", err)
	}
	if got.Prompt != run.Prompt {
		t.Errorf("Prompt = %q, want %q", got.Prompt, run.Prompt)
	}
	if got.Model != run.Model {
		t.Errorf("Model = %q, want %q", got.Model, run.Model)
	}
	if got.Transcript != "" {
		t.Errorf("Transcript = %q, want empty", got.Transcript)
	}
}

func TestMarkRunning(t *testing.T) {
	s := newTestStore(t)

	run := &domain.Run{
		ID:        "test-run-2",
		Status:    domain.StatusQueued,
		Stage:     domain.StageQueued,
		InputType: domain.InputTypeText,
		InputText: "content",
		Engine:    domain.EnginePi,
		Prompt:    "prompt",
	}
	s.CreateRun(run)

	if err := s.MarkRunning("test-run-2"); err != nil {
		t.Fatalf("MarkRunning() error: %v", err)
	}

	got, _ := s.GetRun("test-run-2")
	if got.Status != domain.StatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, domain.StatusRunning)
	}
	if got.Stage != domain.StageStarting {
		t.Errorf("Stage = %q, want %q", got.Stage, domain.StageStarting)
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
}

func TestSaveSucceeded(t *testing.T) {
	s := newTestStore(t)

	run := &domain.Run{
		ID:        "test-run-3",
		Status:    domain.StatusRunning,
		Stage:     domain.StageSummarizing,
		InputType: domain.InputTypeText,
		Engine:    domain.EnginePi,
		Prompt:    "prompt",
	}
	s.CreateRun(run)

	if err := s.SaveSucceeded("test-run-3", "This is the summary", "", "pi --mode json --print"); err != nil {
		t.Fatalf("SaveSucceeded() error: %v", err)
	}

	got, _ := s.GetRun("test-run-3")
	if got.Status != domain.StatusSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, domain.StatusSucceeded)
	}
	if got.Summary != "This is the summary" {
		t.Errorf("Summary = %q, want %q", got.Summary, "This is the summary")
	}
	if got.SummaryChars != 19 {
		t.Errorf("SummaryChars = %d, want %d", got.SummaryChars, 19)
	}
	if got.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set")
	}
}

func TestSaveFailed(t *testing.T) {
	s := newTestStore(t)

	run := &domain.Run{
		ID:        "test-run-4",
		Status:    domain.StatusRunning,
		Stage:     domain.StageSummarizing,
		InputType: domain.InputTypeText,
		Engine:    domain.EngineAgy,
		Prompt:    "prompt",
	}
	s.CreateRun(run)

	err := s.SaveFailed("test-run-4", "engine_failed", "agy exited with status 1", "error output", "agy -p")
	if err != nil {
		t.Fatalf("SaveFailed() error: %v", err)
	}

	got, _ := s.GetRun("test-run-4")
	if got.Status != domain.StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, domain.StatusFailed)
	}
	if got.ErrorCode != "engine_failed" {
		t.Errorf("ErrorCode = %q, want %q", got.ErrorCode, "engine_failed")
	}
	if got.ErrorMessage != "agy exited with status 1" {
		t.Errorf("ErrorMessage = %q", got.ErrorMessage)
	}
}

func TestFailRunIfNotTerminal(t *testing.T) {
	s := newTestStore(t)

	// Run already succeeded — should not change
	run := &domain.Run{
		ID:        "test-run-5",
		Status:    domain.StatusSucceeded,
		Stage:     domain.StageDone,
		InputType: domain.InputTypeText,
		Engine:    domain.EnginePi,
		Prompt:    "prompt",
		Summary:   "existing summary",
	}
	s.CreateRun(run)

	s.FailRunIfNotTerminal("test-run-5", "engine_failed", "should not happen")

	got, _ := s.GetRun("test-run-5")
	if got.Status != domain.StatusSucceeded {
		t.Errorf("Status = %q, want %q (should remain succeeded)", got.Status, domain.StatusSucceeded)
	}
}

func TestSaveTranscript(t *testing.T) {
	s := newTestStore(t)

	run := &domain.Run{
		ID:        "test-run-6",
		Status:    domain.StatusRunning,
		Stage:     domain.StageFetchingTranscript,
		InputType: domain.InputTypeYouTube,
		SourceURL: "https://youtube.com/watch?v=abc",
		Engine:    domain.EnginePi,
		Prompt:    "prompt",
	}
	s.CreateRun(run)

	transcript := "This is the full transcript from the video"
	err := s.SaveTranscript("test-run-6", "abc", "Test Video", transcript)
	if err != nil {
		t.Fatalf("SaveTranscript() error: %v", err)
	}

	got, _ := s.GetRun("test-run-6")
	if got.YouTubeVideoID != "abc" {
		t.Errorf("YouTubeVideoID = %q, want %q", got.YouTubeVideoID, "abc")
	}
	if got.YouTubeTitle != "Test Video" {
		t.Errorf("YouTubeTitle = %q, want %q", got.YouTubeTitle, "Test Video")
	}
	if got.Transcript != transcript {
		t.Errorf("Transcript = %q, want %q", got.Transcript, transcript)
	}
	if got.TranscriptChars != len(transcript) {
		t.Errorf("TranscriptChars = %d, want %d", got.TranscriptChars, len(transcript))
	}
}

func TestFindQueuedRunsWithoutWorkflow(t *testing.T) {
	s := newTestStore(t)

	// Queued without workflow
	s.CreateRun(&domain.Run{
		ID: "queued-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi, Prompt: "p",
	})

	// Queued with workflow — should not be returned
	s.CreateRun(&domain.Run{
		ID: "queued-2", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi, Prompt: "p",
		WorkflowInstanceID: "summary:queued-2",
	})

	// Not queued — should not be returned
	s.CreateRun(&domain.Run{
		ID: "running-1", Status: domain.StatusRunning, Stage: domain.StageStarting,
		InputType: domain.InputTypeText, Engine: domain.EnginePi, Prompt: "p",
	})

	runs, err := s.FindQueuedRunsWithoutWorkflow()
	if err != nil {
		t.Fatalf("FindQueuedRunsWithoutWorkflow() error: %v", err)
	}

	if len(runs) != 1 {
		t.Errorf("got %d runs, want 1", len(runs))
	}
	if len(runs) > 0 && runs[0].ID != "queued-1" {
		t.Errorf("got run ID %q, want %q", runs[0].ID, "queued-1")
	}
}

func TestNew_createsDBFile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file should exist")
	}
}

func TestFindCachedRun_FindsSuccessfulRun(t *testing.T) {
	s := newTestStore(t)

	// Create a successful YouTube run
	s.CreateRun(&domain.Run{
		ID: "run-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: "default",
	})

	since := time.Now().Add(-24 * time.Hour)
	run, err := s.FindCachedRun("abc123", "summary", "pi", "pi-model", since)
	if err != nil {
		t.Fatalf("FindCachedRun() error: %v", err)
	}
	if run == nil {
		t.Fatal("FindCachedRun() = nil, want a run")
	}
	if run.ID != "run-1" {
		t.Errorf("run.ID = %q, want %q", run.ID, "run-1")
	}
}

func TestFindCachedRun_IgnoresFailedRuns(t *testing.T) {
	s := newTestStore(t)

	s.CreateRun(&domain.Run{
		ID: "run-fail", Status: domain.StatusFailed, Stage: domain.StageFailed,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: "default",
	})

	since := time.Now().Add(-24 * time.Hour)
	run, err := s.FindCachedRun("abc123", "summary", "pi", "pi-model", since)
	if err != nil {
		t.Fatalf("FindCachedRun() error: %v", err)
	}
	if run != nil {
		t.Errorf("FindCachedRun() = %q, want nil for failed run", run.ID)
	}
}

func TestFindCachedRun_TTLExpired(t *testing.T) {
	s := newTestStore(t)

	s.CreateRun(&domain.Run{
		ID: "run-old", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: "default",
	})

	// Request with a future since — nothing matches
	since := time.Now().Add(1 * time.Hour)
	run, err := s.FindCachedRun("abc123", "summary", "pi", "pi-model", since)
	if err != nil {
		t.Fatalf("FindCachedRun() error: %v", err)
	}
	if run != nil {
		t.Errorf("FindCachedRun() = %q, want nil for expired TTL", run.ID)
	}
}

func TestFindCachedRun_DifferentFormatMiss(t *testing.T) {
	s := newTestStore(t)

	s.CreateRun(&domain.Run{
		ID: "run-summary", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: "default",
	})

	since := time.Now().Add(-24 * time.Hour)
	run, err := s.FindCachedRun("abc123", "chapters", "pi", "pi-model", since)
	if err != nil {
		t.Fatalf("FindCachedRun() error: %v", err)
	}
	if run != nil {
		t.Errorf("FindCachedRun() = %q, want nil for different format", run.ID)
	}
}

func TestFindInFlightRun_FindsQueued(t *testing.T) {
	s := newTestStore(t)

	s.CreateRun(&domain.Run{
		ID: "run-queued", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: "default",
	})

	run, err := s.FindInFlightRun("abc123", "summary", "pi", "pi-model")
	if err != nil {
		t.Fatalf("FindInFlightRun() error: %v", err)
	}
	if run == nil {
		t.Fatal("FindInFlightRun() = nil, want a run")
	}
	if run.ID != "run-queued" {
		t.Errorf("run.ID = %q, want %q", run.ID, "run-queued")
	}
}

func TestFindInFlightRun_FindsRunning(t *testing.T) {
	s := newTestStore(t)

	s.CreateRun(&domain.Run{
		ID: "run-running", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: "default",
	})

	run, err := s.FindInFlightRun("abc123", "summary", "pi", "pi-model")
	if err != nil {
		t.Fatalf("FindInFlightRun() error: %v", err)
	}
	if run == nil {
		t.Fatal("FindInFlightRun() = nil, want a run")
	}
	if run.ID != "run-running" {
		t.Errorf("run.ID = %q, want %q", run.ID, "run-running")
	}
}

func TestFindInFlightRun_NoMatch(t *testing.T) {
	s := newTestStore(t)

	s.CreateRun(&domain.Run{
		ID: "run-succeeded", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: "default",
	})

	run, err := s.FindInFlightRun("abc123", "summary", "pi", "pi-model")
	if err != nil {
		t.Fatalf("FindInFlightRun() error: %v", err)
	}
	if run != nil {
		t.Errorf("FindInFlightRun() = %q, want nil for completed run", run.ID)
	}
}

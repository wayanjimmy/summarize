package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jimbo/summarize/internal/domain"
	"github.com/jimbo/summarize/internal/engine"
	"github.com/jimbo/summarize/internal/prompt"
	"github.com/jimbo/summarize/internal/youtube"
)

// RunMeta is a lightweight metadata returned by LoadRunActivity.
type RunMeta struct {
	InputType string
}

// MarkRunningActivity sets the run status to running.
func MarkRunningActivity(ctx context.Context, runID string) error {
	slog.Info("MarkRunningActivity", "run_id", runID)
	return deps.Store.MarkRunning(runID)
}

// LoadRunActivity loads run metadata from the app DB.
func LoadRunActivity(ctx context.Context, runID string) (*RunMeta, error) {
	slog.Info("LoadRunActivity", "run_id", runID)
	run, err := deps.Store.GetRun(runID)
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	return &RunMeta{InputType: run.InputType}, nil
}

// FetchTranscriptActivity fetches YouTube transcript and stores it.
func FetchTranscriptActivity(ctx context.Context, runID string) error {
	slog.Info("FetchTranscriptActivity", "run_id", runID)

	if err := deps.Store.SetStage(runID, domain.StageFetchingTranscript); err != nil {
		return fmt.Errorf("set stage: %w", err)
	}

	run, err := deps.Store.GetRun(runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}

	cfg := youtube.YtdlpConfig{
		Binary:          deps.Config.YtdlpBin,
		TranscriptLangs: deps.Config.TranscriptLangs,
		Timeout:         deps.Config.YtdlpTimeout,

	}

	result, err := youtube.FetchTranscript(ctx, cfg, run.SourceURL)
	if err != nil {
		return err
	}

	if result.Transcript == "" {
		return fmt.Errorf("transcript_unavailable")
	}

	// Save plain transcript (existing)
	if err := deps.Store.SaveTranscript(runID, result.VideoID, result.Title, result.Transcript); err != nil {
		return fmt.Errorf("save transcript: %w", err)
	}

	// Save timestamped transcript (new)
	if err := deps.Store.SaveTranscriptWithTimestamps(runID, result.TranscriptWithTimestamps); err != nil {
		return fmt.Errorf("save timestamped transcript: %w", err)
	}

	// Check chapters + long video: reject before SummarizeActivity
	if run.Format == domain.FormatChapters && len(result.TranscriptWithTimestamps) > deps.Config.MaxInputChars {
		return fmt.Errorf("chapters_unsupported_length: video too long for chapters format")
	}

	return nil
}

// SummarizeActivity builds the prompt, runs the engine, and stores the result.
func SummarizeActivity(ctx context.Context, runID string) error {
	slog.Info("SummarizeActivity", "run_id", runID)

	if err := deps.Store.SetStage(runID, domain.StageSummarizing); err != nil {
		return fmt.Errorf("set stage: %w", err)
	}

	run, err := deps.Store.GetRun(runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}

	// Determine content based on format
	content := prompt.SelectContentForFormat(run.Format, run.Transcript, run.TranscriptWithTimestamps, run.InputText)

	// Truncate if too large and save flag
	content, truncated := prompt.Truncate(content, deps.Config.MaxInputChars)
	if truncated {
		_ = deps.Store.SaveTruncated(runID, true)
	}

	// Build prompt using format-aware builder
	var fullPrompt string
	if run.Format == domain.FormatSummary && run.Prompt != "" {
		// Backward compatibility: for summary format with user-provided prompt,
		// use the prompt directly (same as before)
		switch run.InputType {
		case domain.InputTypeYouTube:
			fullPrompt = prompt.BuildYouTube(run.Prompt, run.YouTubeTitle, run.SourceURL, content)
		default:
			fullPrompt = prompt.Build(run.Prompt, content)
		}
	} else if run.Format == domain.FormatSummary {
		// Summary format with default prompt — use the format builder with the default
		fullPrompt = prompt.BuildForFormat(run.Format, "", run.InputType, run.YouTubeTitle, run.SourceURL, content)
	} else {
		// Non-summary formats — format template + user prompt as refinement
		refinement := run.Prompt
		if refinement == "" {
			refinement = ""
		}
		fullPrompt = prompt.BuildForFormat(run.Format, refinement, run.InputType, run.YouTubeTitle, run.SourceURL, content)
	}

	// Create work directory for this run
	workDir := filepath.Join(deps.Config.DataDir, "workdirs", runID)
	os.MkdirAll(workDir, 0755)

	// Select engine
	var eng engine.Engine
	switch run.Engine {
	case domain.EnginePi:
		eng = deps.PiEngine
	case domain.EngineAgy:
		eng = deps.AgyEngine
	default:
		return fmt.Errorf("unknown engine: %s", run.Engine)
	}

	// Run with timeout
	runCtx, cancel := context.WithTimeout(ctx, deps.Config.RunTimeout)
	defer cancel()

	result, err := eng.Run(runCtx, engine.Request{
		RunID:   runID,
		Prompt:  fullPrompt,
		Model:   run.Model,
		WorkDir: workDir,
	})
	if err != nil {
		// Save error details including stderr
		_ = deps.Store.SaveFailed(runID, "engine_failed", err.Error(), result.Stderr, result.Command)
		return err
	}

	// Save success
	return deps.Store.SaveSucceeded(runID, result.Text, result.Stderr, result.Command)
}

// FailRunActivity marks a run as failed.
func FailRunActivity(ctx context.Context, runID, code, message string) error {
	slog.Info("FailRunActivity", "run_id", runID, "code", code, "message", message)
	return deps.Store.FailRunIfNotTerminal(runID, code, message)
}

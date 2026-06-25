package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TranscriptResult holds the fetched transcript and metadata.
type TranscriptResult struct {
	VideoID                  string
	Title                    string
	Transcript               string // plain text (existing)
	TranscriptWithTimestamps string // timestamped text (new)
	Provider                 string
	SourceLanguage           string
	TargetLanguage           string
	Translated               bool
	Generated                bool
}

// YtdlpConfig configures yt-dlp behavior.
type YtdlpConfig struct {
	Binary          string
	TranscriptLangs string
	Timeout         time.Duration
}

// DefaultYtdlpConfig returns sensible defaults.
func DefaultYtdlpConfig() YtdlpConfig {
	return YtdlpConfig{
		Binary:          "yt-dlp",
		TranscriptLangs: "id-orig,id,en.*,en",
		Timeout:         60 * time.Second,
	}
}

// metadata represents the relevant fields from yt-dlp JSON output.
type metadata struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Duration any    `json:"duration"`
}

// FetchTranscript downloads subtitles/captions for a YouTube URL and returns plain text.
func FetchTranscript(ctx context.Context, cfg YtdlpConfig, videoURL string) (*TranscriptResult, error) {
	result, err := fetchTranscriptYtdlp(ctx, cfg, videoURL)
	if err == nil && result.Transcript != "" {
		result.Provider = "yt-dlp"
		return result, nil
	}

	if shouldTryInnerTubeFallback(err) {
		slog.Info("falling back to InnerTube transcript fetcher", "error", err)
		fallback, fallbackErr := fetchTranscriptInnerTube(ctx, innerTubeConfig{
			Timeout:         cfg.Timeout,
			TranscriptLangs: cfg.TranscriptLangs,
		}, videoURL)
		if fallbackErr == nil && fallback.Transcript != "" {
			return fallback, nil
		}
		return nil, fallbackErr
	}

	return nil, err
}

func fetchTranscriptYtdlp(ctx context.Context, cfg YtdlpConfig, videoURL string) (*TranscriptResult, error) {
	// Create temp dir for subtitle output
	tmpDir, err := os.MkdirTemp("", "summarize-ytdlp-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Step 1: Fetch metadata
	meta, err := fetchMetadata(ctx, cfg, videoURL)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata: %w", err)
	}

	// Step 2: Download subtitles
	if err := downloadSubs(ctx, cfg, videoURL, tmpDir); err != nil {
		return nil, fmt.Errorf("download subs: %w", err)
	}

	// Step 3: Find and read VTT files, then parse both ways
	rawVTT, err := findAndReadVTT(tmpDir)
	if err != nil {
		return nil, err
	}

	if rawVTT == "" {
		return nil, fmt.Errorf("transcript_unavailable")
	}

	return &TranscriptResult{
		VideoID:                  meta.ID,
		Title:                    meta.Title,
		Transcript:               ParseVTT(rawVTT),
		TranscriptWithTimestamps: ParseVTTWithTimestamps(rawVTT),
	}, nil
}

func shouldTryInnerTubeFallback(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if hasTranscriptErrorCode(err, ErrTranscriptUnavailable) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "transcript_unavailable") ||
		strings.Contains(msg, "download subs") ||
		strings.Contains(msg, "no captions") ||
		strings.Contains(msg, "subtitle")
}

func fetchMetadata(ctx context.Context, cfg YtdlpConfig, videoURL string) (*metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	args := []string{
		"--dump-single-json",
		"--skip-download",
		"--no-playlist",
	}
	args = append(args, videoURL)
	cmd := exec.CommandContext(ctx, cfg.Binary, args...)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp metadata: %w", err)
	}

	var meta metadata
	if err := json.Unmarshal(output, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}

	return &meta, nil
}

func downloadSubs(ctx context.Context, cfg YtdlpConfig, videoURL, outputDir string) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	outputTpl := filepath.Join(outputDir, "%(id)s.%(ext)s")

	args := []string{
		"--skip-download",
		"--no-playlist",
		"--write-subs",
		"--write-auto-subs",
		"--sub-langs", cfg.TranscriptLangs,
		"--sub-format", "vtt",
		"-o", outputTpl,
	}
	args = append(args, videoURL)
	cmd := exec.CommandContext(ctx, cfg.Binary, args...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("yt-dlp subs: %w", err)
	}

	return nil
}

func findAndReadVTT(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read dir: %w", err)
	}

	var vttFiles []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".vtt") {
			vttFiles = append(vttFiles, filepath.Join(dir, name))
		}
	}

	if len(vttFiles) == 0 {
		return "", nil // No captions found
	}

	data, err := os.ReadFile(vttFiles[0])
	if err != nil {
		return "", fmt.Errorf("read vtt: %w", err)
	}

	return string(data), nil
}

func findAndParseVTT(dir string) (string, error) {
	raw, err := findAndReadVTT(dir)
	if err != nil {
		return "", err
	}
	return ParseVTT(raw), nil
}

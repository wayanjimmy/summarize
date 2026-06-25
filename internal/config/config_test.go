package config

import (
	"os"
	"testing"
)

func TestLoad_defaults(t *testing.T) {
	// Clear any env vars that might interfere
	for _, key := range []string{
		"PORT", "DATA_DIR", "LOG_LEVEL",
		"SUMMARIZE_ENGINE", "SUMMARIZE_PROMPT",
		"SUMMARIZE_PI_BIN", "SUMMARIZE_AGY_BIN",
		"SUMMARIZE_RUN_TIMEOUT", "SUMMARIZE_MAX_INPUT_CHARS",
		"SUMMARIZE_YTDLP_BIN", "SUMMARIZE_TRANSCRIPT_LANGS", "SUMMARIZE_YTDLP_TIMEOUT",
		"NATS_HOST", "NATS_PORT", "NATS_SUBJECT_SUMMARY_REQUESTED",
	} {
		os.Unsetenv(key)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, "8080")
	}
	if cfg.DefaultEngine != "pi" {
		t.Errorf("DefaultEngine = %q, want %q", cfg.DefaultEngine, "pi")
	}
	if cfg.PiBin != "pi" {
		t.Errorf("PiBin = %q, want %q", cfg.PiBin, "pi")
	}
	if cfg.AgyBin != "agy" {
		t.Errorf("AgyBin = %q, want %q", cfg.AgyBin, "agy")
	}
	if cfg.MaxInputChars != 100000 {
		t.Errorf("MaxInputChars = %d, want %d", cfg.MaxInputChars, 100000)
	}
}

func TestLoad_invalidEngine(t *testing.T) {
	os.Setenv("SUMMARIZE_ENGINE", "invalid")
	defer os.Unsetenv("SUMMARIZE_ENGINE")

	_, err := Load()
	if err == nil {
		t.Error("expected error for invalid engine")
	}
}

func TestLoad_customEngine(t *testing.T) {
	os.Setenv("SUMMARIZE_ENGINE", "agy")
	defer os.Unsetenv("SUMMARIZE_ENGINE")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.DefaultEngine != "agy" {
		t.Errorf("DefaultEngine = %q, want %q", cfg.DefaultEngine, "agy")
	}
}

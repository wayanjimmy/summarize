package engine

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// AgyAdapter runs the agy CLI for summarization.
type AgyAdapter struct {
	Binary string
}

// NewAgyAdapter creates a new AgyAdapter.
func NewAgyAdapter(binary string) *AgyAdapter {
	if binary == "" {
		binary = "agy"
	}
	return &AgyAdapter{Binary: binary}
}

// Name returns the engine name.
func (a *AgyAdapter) Name() string {
	return "agy"
}

// ListModels returns models reported by the agy CLI.
func (a *AgyAdapter) ListModels(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, a.Binary, "models")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list agy models: %w", err)
	}
	return ParseModelList(string(out)), nil
}

// Run executes agy and returns the summary.
func (a *AgyAdapter) Run(ctx context.Context, req Request) (Result, error) {
	// Append output instructions so agy prints the full summary to stdout
	// instead of writing to a file on disk.
	prompt := req.Prompt + "\n\nIMPORTANT: Output the full summary directly to stdout as plain text. Do NOT write to any files. Do NOT create any new files."

	args := []string{"-p", prompt}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	cmd := exec.CommandContext(ctx, a.Binary, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	commandStr := a.Binary + " -p <prompt>"
	if req.Model != "" {
		commandStr += " --model " + req.Model
	}

	if runErr != nil {
		stderrStr := stderr.String()
		if len(stderrStr) > 8192 {
			stderrStr = stderrStr[:8192]
		}
		return Result{
			Command: commandStr,
			Stderr:  stderrStr,
		}, fmt.Errorf("agy: %w", runErr)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return Result{
			Command: commandStr,
			Stderr:  stderr.String(),
		}, fmt.Errorf("agy: empty stdout")
	}

	stderrStr := stderr.String()
	if len(stderrStr) > 8192 {
		stderrStr = stderrStr[:8192]
	}

	return Result{
		Text:    text,
		Stderr:  stderrStr,
		Command: commandStr,
	}, nil
}

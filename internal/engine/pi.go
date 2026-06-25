package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PiAdapter runs the pi CLI for summarization.
type PiAdapter struct {
	Binary string
}

// NewPiAdapter creates a new PiAdapter.
func NewPiAdapter(binary string) *PiAdapter {
	if binary == "" {
		binary = "pi"
	}
	return &PiAdapter{Binary: binary}
}

// Name returns the engine name.
func (a *PiAdapter) Name() string {
	return "pi"
}

// ListModels returns models reported by the pi CLI.
func (a *PiAdapter) ListModels(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, a.Binary, "--list-models")
	env := os.Environ()
	env = append(env, "PI_NO_ANIMATION=1")
	cmd.Env = env

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list pi models: %w", err)
	}
	return ParseModelList(string(out)), nil
}

// Run executes pi and returns the summary.
func (a *PiAdapter) Run(ctx context.Context, req Request) (Result, error) {
	args := []string{"--mode", "json", "--print"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	cmd := exec.CommandContext(ctx, a.Binary, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	// Inherit env and add PI_NO_ANIMATION
	env := os.Environ()
	env = append(env, "PI_NO_ANIMATION=1")
	cmd.Env = env

	// Write prompt to stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start pi: %w", err)
	}

	// Write prompt in background
	go func() {
		defer stdin.Close()
		stdin.Write([]byte(req.Prompt + "\n"))
	}()

	// Parse JSONL from stdout
	stdoutScanner := bufio.NewScanner(stdout)

	// Read stderr in background (capped at 8KB)
	var stderrBuf strings.Builder
	stderrScanner := bufio.NewScanner(stderr)
	go func() {
		for stderrScanner.Scan() {
			if stderrBuf.Len() < 8192 {
				stderrBuf.WriteString(stderrScanner.Text())
				stderrBuf.WriteString("\n")
			}
		}
	}()

	// Parse output (blocks until pi finishes)
	text, parseErr := parsePiJSONL(stdoutScanner)

	// Wait for pi to exit
	runErr := cmd.Wait()

	commandStr := a.Binary + " " + strings.Join(args, " ")

	if runErr != nil {
		return Result{
			Command: commandStr,
			Stderr:  stderrBuf.String(),
		}, fmt.Errorf("%w", piExitError(runErr))
	}

	if parseErr != nil {
		return Result{
			Command: commandStr,
			Stderr:  stderrBuf.String(),
		}, parseErr
	}

	return Result{
		Text:    text,
		Stderr:  stderrBuf.String(),
		Command: commandStr,
	}, nil
}

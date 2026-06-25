// Package engine provides LLM engine adapters for summarization.
package engine

import "context"

// Engine is the interface for LLM backends.
type Engine interface {
	Name() string
	Run(ctx context.Context, req Request) (Result, error)
}

// ModelLister is implemented by engines that can list available models.
type ModelLister interface {
	Name() string
	ListModels(ctx context.Context) ([]string, error)
}

// Request holds the input for an engine run.
type Request struct {
	RunID   string
	Prompt  string
	Model   string
	WorkDir string
}

// Result holds the output from an engine run.
type Result struct {
	Text    string
	Stderr  string
	Command string
}

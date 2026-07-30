package summary

import (
	"time"
)

// SubmitRequest is the protocol-neutral input for creating a summary.
type SubmitRequest struct {
	URL    string
	Text   string
	Engine string
	Model  string
	Prompt string
	Format string

	OwnerID        string // authenticated principal who owns this run
	IdempotencyKey string // client-supplied key for stateless retry dedup
}

// SubmitResult is the protocol-neutral output of a summary submission.
type SubmitResult struct {
	RunID  string
	Status string
	Cached bool
}

// UpdateRequest is the protocol-neutral input for updating a queued task.
// Only queued runs can be updated; the prompt can be refined before the
// workflow picks up the run.
type UpdateRequest struct {
	RunID   string
	OwnerID string
	Prompt  string
}

// EngineModelInfo describes model availability for one engine.
type EngineModelInfo struct {
	Models       []string
	DefaultModel string
	Status       string // "available", "unavailable", "stale"
	Available    bool
	Stale        bool
	FetchedAt    time.Time
	Error        string
}

// ListModelsResult is the protocol-neutral output of listing models.
type ListModelsResult struct {
	Engines map[string]EngineModelInfo
}

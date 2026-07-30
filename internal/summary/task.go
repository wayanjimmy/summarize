package summary

import (
	"time"

	"github.com/wayanjimmy/summarize/internal/domain"
)

// Task is the application-level task projection from a Run.
// It is independent of the MCP transport and will be exposed by Phase 5
// as the native Tasks transport. The projection carries the lifecycle state
// and result/error without transport-specific formatting.
type Task struct {
	ID           string
	Status       string
	Stage        string
	InputType    string
	Engine       string
	Model        string
	Format       string
	Summary      string
	ErrorCode    string
	ErrorMessage string
	CreatedAt    time.Time
	StartedAt    time.Time
	FinishedAt   time.Time
}

// IsTerminal returns true if the task is in a terminal state
// (succeeded, failed, or cancelled).
func (t Task) IsTerminal() bool {
	return domain.IsTerminalStatus(t.Status)
}

// IsCancellable returns true if the task can be cancelled
// (queued or running).
func (t Task) IsCancellable() bool {
	return domain.IsCancellableStatus(t.Status)
}

// taskFromRun projects a domain.Run into a Task.
func taskFromRun(r *domain.Run) Task {
	return Task{
		ID:           r.ID,
		Status:       r.Status,
		Stage:        r.Stage,
		InputType:    r.InputType,
		Engine:       r.Engine,
		Model:        r.Model,
		Format:       r.Format,
		Summary:      r.Summary,
		ErrorCode:    r.ErrorCode,
		ErrorMessage: r.ErrorMessage,
		CreatedAt:    r.CreatedAt,
		StartedAt:    r.StartedAt,
		FinishedAt:   r.FinishedAt,
	}
}

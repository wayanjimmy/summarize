// Package domain defines the core types for the summarize service.
package domain

import "time"

// Run status constants.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// Stage constants track workflow progress.
const (
	StageQueued             = "queued"
	StageStarting           = "starting"
	StageFetchingTranscript = "fetching_transcript"
	StageSummarizing        = "summarizing"
	StageDone               = "done"
	StageFailed             = "failed"
)

// Input type constants.
const (
	InputTypeYouTube = "youtube"
	InputTypeText    = "text"
)

// Format constants for output shape.
const (
	FormatSummary  = "summary"
	FormatChapters = "chapters"
	FormatThread   = "thread"
	FormatBlog     = "blog"
)

// ValidFormats is the set of accepted format values.
var ValidFormats = map[string]bool{
	FormatSummary:  true,
	FormatChapters: true,
	FormatThread:   true,
	FormatBlog:     true,
}

// Engine constants.
const (
	EnginePi  = "pi"
	EngineAgy = "agy"
)

// Run represents a summarization run in the app database.
type Run struct {
	ID string

	Status string
	Stage  string

	InputType string
	SourceURL string
	InputText string

	YouTubeVideoID string
	YouTubeTitle   string

	Engine string
	Model  string
	Prompt string

	Format                        string // "summary", "chapters", "thread", "blog"
	Truncated                     bool   // whether transcript was truncated
	TranscriptWithTimestamps      string // VTT-style transcript with time cues
	TranscriptWithTimestampsChars int    // character count

	Transcript      string
	TranscriptChars int

	Summary      string
	SummaryChars int

	ErrorCode    string
	ErrorMessage string

	Stderr  string
	Command string

	EventID          string
	EventPublishedAt time.Time

	WorkflowInstanceID  string
	WorkflowExecutionID string

	CreatedAt  time.Time
	UpdatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// RunError is a structured error stored in the database.
type RunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is the standard JSON error response shape.
type APIError struct {
	Error RunError `json:"error"`
}

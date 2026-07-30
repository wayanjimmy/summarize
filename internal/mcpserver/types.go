// Package mcpserver exposes the summarize service over the MCP 2026-07-28
// stateless Streamable HTTP transport.
package mcpserver

// SummarizeInput is the typed argument set for the "summarize" tool.
// The SDK derives a JSON Schema from these struct tags.
type SummarizeInput struct {
	URL            string `json:"url,omitempty" jsonschema:"a YouTube URL to summarize"`
	Text           string `json:"text,omitempty" jsonschema:"raw text to summarize"`
	Engine         string `json:"engine,omitempty" jsonschema:"LLM engine to use: 'pi' or 'agy' (defaults to server default)"`
	Model          string `json:"model,omitempty" jsonschema:"specific model to use (see summarize://models resource for available models)"`
	Prompt         string `json:"prompt,omitempty" jsonschema:"custom prompt to override the default (max 20000 chars)"`
	Format         string `json:"format,omitempty" jsonschema:"output format: 'summary', 'chapters', 'thread', or 'blog' (defaults to 'summary')"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"optional client-supplied key to prevent duplicate work under retries"`
}

// SummarizeOutput is the structured output of the "summarize" tool.
type SummarizeOutput struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Cached bool   `json:"cached,omitempty"`
}

// GetSummaryInput is the typed argument set for the "get_summary" tool.
type GetSummaryInput struct {
	RunID string `json:"run_id" jsonschema:"the run ID returned by the summarize tool"`
}

// CancelSummaryInput is the typed argument set for the "cancel_summary" tool.
type CancelSummaryInput struct {
	RunID string `json:"run_id" jsonschema:"the run ID to cancel"`
}

// UpdateSummaryInput is the typed argument set for the "update_summary" tool.
type UpdateSummaryInput struct {
	RunID  string `json:"run_id" jsonschema:"the run ID to update (must be queued)"`
	Prompt string `json:"prompt,omitempty" jsonschema:"new prompt to replace the original (max 20000 chars)"`
}

// GetSummaryOutput is the structured output of the "get_summary" tool.
type GetSummaryOutput struct {
	RunID          string  `json:"run_id"`
	Status         string  `json:"status"`
	Stage          string  `json:"stage"`
	InputType      string  `json:"input_type"`
	Engine         string  `json:"engine"`
	Model          string  `json:"model,omitempty"`
	Format         string  `json:"format"`
	Summary        string  `json:"summary,omitempty"`
	Truncated      bool    `json:"truncated,omitempty"`
	TranscriptChars int    `json:"transcript_chars,omitempty"`
	SummaryChars   int     `json:"summary_chars,omitempty"`
	ErrorCode      string  `json:"error_code,omitempty"`
	ErrorMessage   string  `json:"error_message,omitempty"`
	CreatedAt      string  `json:"created_at"`
	StartedAt      string  `json:"started_at,omitempty"`
	FinishedAt     string  `json:"finished_at,omitempty"`
}

// ModelsResource is the JSON shape of the summarize://models resource.
type ModelsResource struct {
	Engines map[string]EngineModels `json:"engines"`
}

// TaskOutput is the structured output of cancel/update tools.
type TaskOutput struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Stage  string `json:"stage"`
}

type EngineModels struct {
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model,omitempty"`
	Status       string   `json:"status"`
	Available    bool     `json:"available"`
	Stale        bool     `json:"stale,omitempty"`
	FetchedAt    string   `json:"fetched_at,omitempty"`
	Error        string   `json:"error,omitempty"`
}

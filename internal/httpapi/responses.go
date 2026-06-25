// Package httpapi provides the REST API for the summarize service.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/wayanjimmy/summarize/internal/domain"
)

// ErrorResponse is the standard error JSON shape.
type ErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// CreateSummaryResponse is returned when a new summary is queued.
type CreateSummaryResponse struct {
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url"`
	ResultURL string `json:"result_url"`
	Cached    bool   `json:"cached,omitempty"`
}

// ModelsResponse is returned by GET /v1/models.
type ModelsResponse struct {
	Engines map[string]EngineModelsResponse `json:"engines"`
}

// EngineModelsResponse describes model availability for one engine.
type EngineModelsResponse struct {
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model,omitempty"`
	Status       string   `json:"status"`
	Available    bool     `json:"available"`
	Stale        bool     `json:"stale"`
	FetchedAt    string   `json:"fetched_at,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// StatusResponse is returned by GET /v1/runs/{run_id}/status.
type StatusResponse struct {
	RunID     string           `json:"run_id"`
	Status    string           `json:"status"`
	Stage     string           `json:"stage"`
	Error     *domain.RunError `json:"error,omitempty"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
}

// SummaryResponse is returned by GET /v1/summaries/{run_id}.
type SummaryResponse struct {
	RunID           string           `json:"run_id"`
	Status          string           `json:"status"`
	Stage           string           `json:"stage"`
	InputType       string           `json:"input_type"`
	SourceURL       string           `json:"source_url,omitempty"`
	YouTube         *YouTubeInfo     `json:"youtube,omitempty"`
	Engine          string           `json:"engine"`
	Prompt          string           `json:"prompt"`
	Format          string           `json:"format"`
	Truncated       bool             `json:"truncated,omitempty"`
	Summary         string           `json:"summary,omitempty"`
	TranscriptChars int              `json:"transcript_chars,omitempty"`
	SummaryChars    int              `json:"summary_chars,omitempty"`
	Error           *domain.RunError `json:"error,omitempty"`
	CreatedAt       string           `json:"created_at"`
	StartedAt       string           `json:"started_at,omitempty"`
	FinishedAt      string           `json:"finished_at,omitempty"`
}

// YouTubeInfo holds YouTube-specific metadata in the response.
type YouTubeInfo struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	resp := ErrorResponse{}
	resp.Error.Code = code
	resp.Error.Message = message
	writeJSON(w, status, resp)
}

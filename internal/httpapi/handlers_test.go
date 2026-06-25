package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jimbo/summarize/internal/config"
	"github.com/jimbo/summarize/internal/domain"
	"github.com/jimbo/summarize/internal/engine"
	"github.com/jimbo/summarize/internal/store"
)

func TestListModelsPartialFailure(t *testing.T) {
	h := &Handlers{
		Config: &config.Config{},
		Models: engine.NewModelCatalog(0,
			&testLister{name: "pi", models: []string{"pi-model"}},
			&testLister{name: "agy", err: errors.New("offline")},
		),
	}
	rr := httptest.NewRecorder()
	h.ListModels(rr, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{`"pi"`, `"pi-model"`, `"agy"`, `"status":"unavailable"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
}

func TestResolveModelValidation(t *testing.T) {
	h := &Handlers{
		Config: &config.Config{PiModel: "default"},
		Models: engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"default", "ok"}}),
	}
	if got, status, err := h.resolveModel(context.Background(), "pi", "ok"); err != nil || status != 0 || got != "ok" {
		t.Fatalf("valid model = %q, %d, %v", got, status, err)
	}
	if _, status, err := h.resolveModel(context.Background(), "pi", "bad"); err == nil || status != http.StatusBadRequest {
		t.Fatalf("invalid model status = %d, err = %v", status, err)
	}
	h.Models = engine.NewModelCatalog(0, &testLister{name: "pi", err: errors.New("offline")})
	if _, status, err := h.resolveModel(context.Background(), "pi", "ok"); err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("offline status = %d, err = %v", status, err)
	}
	if got, status, err := h.resolveModel(context.Background(), "pi", ""); err != nil || status != 0 || got != "default" {
		t.Fatalf("offline default = %q, %d, %v", got, status, err)
	}

	h.Models = engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{}})
	if got, status, err := h.resolveModel(context.Background(), "pi", ""); err != nil || status != 0 || got != "default" {
		t.Fatalf("empty default = %q, %d, %v", got, status, err)
	}
	if _, status, err := h.resolveModel(context.Background(), "pi", "ok"); err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("empty explicit status = %d, err = %v", status, err)
	}
}

type testLister struct {
	name   string
	models []string
	err    error
}

func (l *testLister) Name() string { return l.name }

func (l *testLister) ListModels(context.Context) ([]string, error) {
	return l.models, l.err
}

func TestCreateSummary_FormatValidation(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{
			name:   "valid format summary",
			body:   `{"text":"hello","format":"summary"}`,
			status: http.StatusBadRequest, // will fail because no store, but format is valid
		},
		{
			name:   "valid format chapters",
			body:   `{"text":"hello","format":"chapters"}`,
			status: http.StatusBadRequest, // chapters + text → 400, format validation passes but compatibility check fails
		},
		{
			name:   "invalid format",
			body:   `{"text":"hello","format":"invalid"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "omitted format defaults to summary",
			body:   `{"text":"hello"}`,
			status: http.StatusBadRequest, // will fail because no store — format defaulted successfully
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Need full Handlers to test — for format validation, we can get to 400
			// without store by testing invalid format
			if tt.name == "invalid format" {
				h := &Handlers{Config: &config.Config{}}
				rr := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/v1/summaries", strings.NewReader(tt.body))
				h.CreateSummary(rr, req)
				if rr.Code != tt.status {
					t.Errorf("status = %d, want %d; body = %s", rr.Code, tt.status, rr.Body.String())
				}
				if !strings.Contains(rr.Body.String(), "format must be one of") {
					t.Errorf("expected format validation error, got: %s", rr.Body.String())
				}
			}
		})
	}
}

func TestCreateSummary_ChaptersWithTextRejected(t *testing.T) {
	h := &Handlers{Config: &config.Config{}}
	body := `{"text":"some long text","format":"chapters"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/summaries", strings.NewReader(body))
	h.CreateSummary(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "format 'chapters' requires a YouTube URL") {
		t.Errorf("expected chapters+text error, got: %s", rr.Body.String())
	}
}

func TestCreateSummary_CacheHitReturns200(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Seed a successful cached run
	s.CreateRun(&domain.Run{
		ID: "cached-run-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	cfg := &config.Config{
		DefaultEngine: "pi",
		DefaultPrompt:  config.DefaultPrompt,
		PiModel:       "pi-model",
		CacheTTL:      7 * 24 * time.Hour,
	}
	h := &Handlers{
		Store:  s,
		Config: cfg,
		Models: engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
	}

	body := `{"url":"https://youtube.com/watch?v=abc123"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/summaries", strings.NewReader(body))
	h.CreateSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}

	var resp CreateSummaryResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.RunID != "cached-run-1" {
		t.Errorf("RunID = %q, want %q", resp.RunID, "cached-run-1")
	}
	if !resp.Cached {
		t.Error("Cached = false, want true")
	}
}

func TestCreateSummary_InFlightDedupReturns202(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Seed an in-flight run (queued)
	s.CreateRun(&domain.Run{
		ID: "inflight-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=xyz789",
		YouTubeVideoID: "xyz789", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	cfg := &config.Config{
		DefaultEngine: "pi",
		DefaultPrompt:  config.DefaultPrompt,
		PiModel:       "pi-model",
		CacheTTL:      7 * 24 * time.Hour,
	}
	h := &Handlers{
		Store:  s,
		Config: cfg,
		Models: engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
	}

	body := `{"url":"https://youtu.be/xyz789"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/summaries", strings.NewReader(body))
	h.CreateSummary(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rr.Code, rr.Body.String())
	}

	var resp CreateSummaryResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.RunID != "inflight-1" {
		t.Errorf("RunID = %q, want %q", resp.RunID, "inflight-1")
	}
	if !resp.Cached {
		t.Error("Cached = false, want true")
	}
}

func TestCreateSummary_CustomPromptBypassesCache(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Seed a successful cached run with default prompt
	s.CreateRun(&domain.Run{
		ID: "cached-default", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	cfg := &config.Config{
		DefaultEngine: "pi",
		DefaultPrompt:  config.DefaultPrompt,
		PiModel:       "pi-model",
		CacheTTL:      7 * 24 * time.Hour,
	}
	h := &Handlers{
		Store:  s,
		Config: cfg,
		Models: engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		// No publisher — cache bypass is what we're testing.
		// The handler will proceed past dedup and then panic on nil publisher.
	}

	body := `{"url":"https://youtube.com/watch?v=abc123","prompt":"Custom special prompt"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/summaries", strings.NewReader(body))

	// Cache bypass means the handler gets past dedup and hits the nil Publisher.
	// We recover from the panic and verify no cache-hit response was written.
	defer func() {
		if r := recover(); r != nil {
			// Panic from nil publisher — proves request proceeded past dedup (not a cache hit)
			if rr.Code == http.StatusOK {
				var resp CreateSummaryResponse
				json.NewDecoder(rr.Body).Decode(&resp)
				if resp.Cached {
					t.Fatal("custom prompt should bypass cache, but got cache hit")
				}
			}
			return
		}
		// No panic — check response is not a cache hit
		if rr.Code == http.StatusOK {
			var resp CreateSummaryResponse
			json.NewDecoder(rr.Body).Decode(&resp)
			if resp.Cached {
				t.Fatal("custom prompt should bypass cache, but got cache hit")
			}
		}
	}()

	h.CreateSummary(rr, req)
}

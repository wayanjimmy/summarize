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

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/engine"
	"github.com/wayanjimmy/summarize/internal/events"
	"github.com/wayanjimmy/summarize/internal/store"
	"github.com/wayanjimmy/summarize/internal/summary"
)

type testLister struct {
	name   string
	models []string
	err    error
}

func (l *testLister) Name() string { return l.name }

func (l *testLister) ListModels(context.Context) ([]string, error) {
	return l.models, l.err
}

type mockPublisher struct {
	err error
}

func (m *mockPublisher) PublishSummaryRequested(runID string) (*events.SummaryRequested, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &events.SummaryRequested{
		EventID:   uuid.NewString(),
		EventType: "summary.requested",
		RunID:     runID,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// newReqWithChiParam creates a request with chi URL params set, since
// chi.URLParam requires a chi route context.
func newReqWithChiParam(method, target string, body string, runID string) *http.Request {
	var bodyReader strings.Reader
	if body != "" {
		bodyReader = *strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, &bodyReader)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("run_id", runID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
}

func TestListModelsPartialFailure(t *testing.T) {
	h := &Handlers{
		Service: summary.NewService(nil, nil,
			engine.NewModelCatalog(0,
				&testLister{name: "pi", models: []string{"pi-model"}},
				&testLister{name: "agy", err: errors.New("offline")},
			),
			summary.ServiceConfig{},
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

func TestCreateSummary_FormatValidation(t *testing.T) {
	h := &Handlers{
		Service: summary.NewService(nil, nil, nil, summary.ServiceConfig{}),
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/summaries", strings.NewReader(`{"text":"hello","format":"invalid"}`))
	h.CreateSummary(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "format must be one of") {
		t.Errorf("expected format validation error, got: %s", rr.Body.String())
	}
}

func TestCreateSummary_ChaptersWithTextRejected(t *testing.T) {
	h := &Handlers{
		Service: summary.NewService(nil, nil, nil, summary.ServiceConfig{}),
	}
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

	s.CreateRun(&domain.Run{
		ID: "cached-run-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	h := &Handlers{
		Service: summary.NewService(s, nil,
			engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
			summary.ServiceConfig{
				DefaultEngine: "pi",
				DefaultPrompt: config.DefaultPrompt,
				PiModel:       "pi-model",
				CacheTTL:      7 * 24 * time.Hour,
			},
		),
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

	s.CreateRun(&domain.Run{
		ID: "inflight-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=xyz789",
		YouTubeVideoID: "xyz789", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	h := &Handlers{
		Service: summary.NewService(s, nil,
			engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
			summary.ServiceConfig{
				DefaultEngine: "pi",
				DefaultPrompt: config.DefaultPrompt,
				PiModel:       "pi-model",
				CacheTTL:      7 * 24 * time.Hour,
			},
		),
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

	s.CreateRun(&domain.Run{
		ID: "cached-default", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	h := &Handlers{
		Service: summary.NewService(s, &mockPublisher{},
			engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
			summary.ServiceConfig{
				DefaultEngine: "pi",
				DefaultPrompt: config.DefaultPrompt,
				PiModel:       "pi-model",
				CacheTTL:      7 * 24 * time.Hour,
			},
		),
	}

	body := `{"url":"https://youtube.com/watch?v=abc123","prompt":"Custom special prompt"}`
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
	if resp.Cached {
		t.Fatal("custom prompt should bypass cache, but got cache hit")
	}
}

// --- Cancel handler tests ---

func TestCancelRun_QueuedRun(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	s.CreateRun(&domain.Run{
		ID: "queued-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	h := &Handlers{Service: summary.NewService(s, nil, nil, summary.ServiceConfig{})}
	rr := httptest.NewRecorder()
	req := newReqWithChiParam(http.MethodDelete, "/v1/runs/queued-1", "", "queued-1")
	h.CancelRun(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}

	var resp TaskResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Status != domain.StatusCancelled {
		t.Errorf("Status = %q, want %q", resp.Status, domain.StatusCancelled)
	}
	if resp.Message != "run cancelled" {
		t.Errorf("Message = %q, want %q", resp.Message, "run cancelled")
	}
}

func TestCancelRun_SucceededRejected(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	s.CreateRun(&domain.Run{
		ID: "done-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", Summary: "result",
	})

	h := &Handlers{Service: summary.NewService(s, nil, nil, summary.ServiceConfig{})}
	rr := httptest.NewRecorder()
	req := newReqWithChiParam(http.MethodDelete, "/v1/runs/done-1", "", "done-1")
	h.CancelRun(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}

func TestCancelRun_NotFound(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	h := &Handlers{Service: summary.NewService(s, nil, nil, summary.ServiceConfig{})}
	rr := httptest.NewRecorder()
	req := newReqWithChiParam(http.MethodDelete, "/v1/runs/nonexistent", "", "nonexistent")
	h.CancelRun(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rr.Code, rr.Body.String())
	}
}

// --- Update handler tests ---

func TestUpdateRun_QueuedPrompt(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	s.CreateRun(&domain.Run{
		ID: "queued-1", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old prompt",
	})

	h := &Handlers{Service: summary.NewService(s, nil, nil, summary.ServiceConfig{})}
	rr := httptest.NewRecorder()
	req := newReqWithChiParam(http.MethodPatch, "/v1/runs/queued-1", `{"prompt":"new prompt"}`, "queued-1")
	h.UpdateRun(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}

	var resp TaskResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Status != domain.StatusQueued {
		t.Errorf("Status = %q, want %q", resp.Status, domain.StatusQueued)
	}

	// Verify prompt was persisted
	run, _ := s.GetRun("queued-1")
	if run.Prompt != "new prompt" {
		t.Errorf("Prompt = %q, want %q", run.Prompt, "new prompt")
	}
}

func TestUpdateRun_RunningRejected(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	s.CreateRun(&domain.Run{
		ID: "running-1", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old",
	})

	h := &Handlers{Service: summary.NewService(s, nil, nil, summary.ServiceConfig{})}
	rr := httptest.NewRecorder()
	req := newReqWithChiParam(http.MethodPatch, "/v1/runs/running-1", `{"prompt":"new"}`, "running-1")
	h.UpdateRun(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateRun_NotFound(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	h := &Handlers{Service: summary.NewService(s, nil, nil, summary.ServiceConfig{})}
	rr := httptest.NewRecorder()
	req := newReqWithChiParam(http.MethodPatch, "/v1/runs/nonexistent", `{"prompt":"new"}`, "nonexistent")
	h.UpdateRun(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rr.Code, rr.Body.String())
	}
}

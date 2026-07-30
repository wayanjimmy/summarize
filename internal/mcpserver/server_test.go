package mcpserver

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

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/engine"
	"github.com/wayanjimmy/summarize/internal/events"
	"github.com/wayanjimmy/summarize/internal/mcpauth"
	"github.com/wayanjimmy/summarize/internal/store"
	"github.com/wayanjimmy/summarize/internal/summary"
)

// --- test helpers ---

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

func newTestService(t *testing.T) (*summary.Service, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := summary.NewService(st, &mockPublisher{},
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		summary.ServiceConfig{
			DefaultEngine: "pi",
			DefaultPrompt: config.DefaultPrompt,
			PiModel:       "pi-model",
			CacheTTL:      7 * 24 * time.Hour,
		},
	)
	return svc, st
}

// newTestHandler wraps the MCP handler with "none" auth mode so tool
// handlers receive a default principal in the request context.
func newTestHandler(svc *summary.Service) http.Handler {
	raw := NewHandler(svc, "test")
	return mcpauth.Middleware(mcpauth.Config{Mode: mcpauth.AuthModeNone})(raw)
}

// connectClient creates an httptest server from the MCP handler and connects
// a real MCP client to it, returning the session.
func connectClient(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	transport := &mcp.StreamableClientTransport{
		Endpoint:              httpServer.URL,
		DisableStandaloneSSE:  true,
	}

	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-client", Version: "v0.0.0"},
		&mcp.ClientOptions{
			Capabilities: &mcp.ClientCapabilities{},
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect failed: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	return session
}

func textContent(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("len(Content) = 0, want at least 1")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T, want *TextContent", res.Content[0])
	}
	return text.Text
}

// --- tests ---

func TestListTools(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	if !names["summarize"] {
		t.Error("missing 'summarize' tool")
	}
	if !names["get_summary"] {
		t.Error("missing 'get_summary' tool")
	}
}

func TestListResources(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}

	found := false
	for _, res := range result.Resources {
		if res.URI == "summarize://models" {
			found = true
			if res.MIMEType != "application/json" {
				t.Errorf("MIMEType = %q, want application/json", res.MIMEType)
			}
		}
	}
	if !found {
		t.Error("missing summarize://models resource")
	}
}

func TestSummarizeTool_Text(t *testing.T) {
	svc, st := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "some text to summarize"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	text := textContent(t, result)
	if !strings.Contains(text, "Run ID:") {
		t.Errorf("response missing Run ID: %s", text)
	}

	// Verify run was persisted
	runs, _ := st.FindQueuedRunsWithoutWorkflow()
	if len(runs) != 1 {
		t.Errorf("expected 1 queued run in store, got %d", len(runs))
	}
}

func TestSummarizeTool_YouTube(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"url": "https://youtube.com/watch?v=dQw4w9WgXcQ"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	text := textContent(t, result)
	if !strings.Contains(text, "Run ID:") {
		t.Errorf("response missing Run ID: %s", text)
	}
}

func TestSummarizeTool_InvalidInput(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Neither url nor text
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error for empty input, got: %s", textContent(t, result))
	}
	if !strings.Contains(textContent(t, result), "exactly one of url or text") {
		t.Errorf("error message = %q, want validation error", textContent(t, result))
	}
}

func TestSummarizeTool_CacheHit(t *testing.T) {
	svc, st := newTestService(t)

	// Seed a successful cached run
	st.CreateRun(&domain.Run{
		ID: "cached-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"url": "https://youtube.com/watch?v=abc123"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	text := textContent(t, result)
	if !strings.Contains(text, "cached") {
		t.Errorf("expected cached response, got: %s", text)
	}
	if !strings.Contains(text, "cached-1") {
		t.Errorf("expected cached run ID, got: %s", text)
	}
}

func TestGetSummaryTool_Queued(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-queued", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": "run-queued"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	text := textContent(t, result)
	if !strings.Contains(text, "queued") {
		t.Errorf("expected status 'queued' in response, got: %s", text)
	}
}

func TestGetSummaryTool_Succeeded(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-done", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", Summary: "This is the summary.",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": "run-done"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	text := textContent(t, result)
	if text != "This is the summary." {
		t.Errorf("expected summary text, got: %s", text)
	}
}

func TestGetSummaryTool_Failed(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-failed", Status: domain.StatusFailed, Stage: domain.StageFailed,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
		ErrorCode: "engine_failed", ErrorMessage: "connection refused",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": "run-failed"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for failed run, got: %s", textContent(t, result))
	}

	text := textContent(t, result)
	if !strings.Contains(text, "engine_failed") {
		t.Errorf("expected error code in response, got: %s", text)
	}
	if !strings.Contains(text, "connection refused") {
		t.Errorf("expected error message in response, got: %s", text)
	}
}

func TestGetSummaryTool_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": "nonexistent"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for not found, got: %s", textContent(t, result))
	}
	if !strings.Contains(textContent(t, result), "not found") {
		t.Errorf("expected not found error, got: %s", textContent(t, result))
	}
}

func TestModelsResource(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "summarize://models",
	})
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}

	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Contents))
	}

	var models ModelsResource
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &models); err != nil {
		t.Fatalf("failed to parse models JSON: %v", err)
	}

	piInfo, ok := models.Engines["pi"]
	if !ok {
		t.Fatal("missing 'pi' engine in models resource")
	}
	if len(piInfo.Models) != 1 || piInfo.Models[0] != "pi-model" {
		t.Errorf("pi models = %v, want [pi-model]", piInfo.Models)
	}
	if piInfo.Status != "available" {
		t.Errorf("pi status = %q, want available", piInfo.Status)
	}
}

func TestSummarizeTool_PublishFailure(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := summary.NewService(st, &mockPublisher{err: errors.New("nats down")},
		engine.NewModelCatalog(0, &testLister{name: "pi", models: []string{"pi-model"}}),
		summary.ServiceConfig{
			DefaultEngine: "pi",
			DefaultPrompt: config.DefaultPrompt,
			PiModel:       "pi-model",
			CacheTTL:      7 * 24 * time.Hour,
		},
	)

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "some text"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for publish failure, got: %s", textContent(t, result))
	}
}

// --- cancel_summary tool tests ---

func TestCancelSummaryTool_QueuedRun(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-queued", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "cancel_summary",
		Arguments: map[string]any{"run_id": "run-queued"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	text := textContent(t, result)
	if !strings.Contains(text, "cancelled") {
		t.Errorf("expected 'cancelled' in response, got: %s", text)
	}

	// Verify run was cancelled in store
	run, _ := st.GetRun("run-queued")
	if run.Status != domain.StatusCancelled {
		t.Errorf("store status = %q, want %q", run.Status, domain.StatusCancelled)
	}
}

func TestCancelSummaryTool_SucceededRejected(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-done", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test", Summary: "result",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "cancel_summary",
		Arguments: map[string]any{"run_id": "run-done"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for succeeded run, got: %s", textContent(t, result))
	}
}

func TestCancelSummaryTool_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "cancel_summary",
		Arguments: map[string]any{"run_id": "nonexistent"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for not found, got: %s", textContent(t, result))
	}
	if !strings.Contains(textContent(t, result), "not found") {
		t.Errorf("expected not found error, got: %s", textContent(t, result))
	}
}

func TestListTools_IncludesCancelAndUpdate(t *testing.T) {
	svc, _ := newTestService(t)
	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"summarize", "get_summary", "cancel_summary", "update_summary"} {
		if !names[want] {
			t.Errorf("missing %q tool", want)
		}
	}
}

// --- update_summary tool tests ---

func TestUpdateSummaryTool_QueuedRun(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-queued", Status: domain.StatusQueued, Stage: domain.StageQueued,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old prompt",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "update_summary",
		Arguments: map[string]any{"run_id": "run-queued", "prompt": "new prompt"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	// Verify prompt was persisted
	run, _ := st.GetRun("run-queued")
	if run.Prompt != "new prompt" {
		t.Errorf("Prompt = %q, want %q", run.Prompt, "new prompt")
	}
}

func TestUpdateSummaryTool_RunningRejected(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-running", Status: domain.StatusRunning, Stage: domain.StageSummarizing,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "old",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "update_summary",
		Arguments: map[string]any{"run_id": "run-running", "prompt": "new"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for running run, got: %s", textContent(t, result))
	}
}

func TestGetSummaryTool_Cancelled(t *testing.T) {
	svc, st := newTestService(t)

	st.CreateRun(&domain.Run{
		ID: "run-cancelled", Status: domain.StatusCancelled, Stage: domain.StageCancelled,
		InputType: domain.InputTypeText, Engine: domain.EnginePi,
		Format: "summary", Prompt: "test",
	})

	handler := newTestHandler(svc)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": "run-cancelled"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected IsError for cancelled run, got: %s", textContent(t, result))
	}
	if !strings.Contains(textContent(t, result), "cancelled") {
		t.Errorf("expected 'cancelled' in response, got: %s", textContent(t, result))
	}
}

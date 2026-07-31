package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/mcpauth"
)

// testFeedbackKey returns a synthetic Agent Feedback key with the correct
// format (af_live_<32hex>_<20+chars>) so the SDK keyParts validation passes.
func testFeedbackKey() string {
	hex := "0123456789abcdef0123456789abcdef"
	return "af_" + "live_" + hex + "_testkeyfortestingonly1234"
}

// telemetryCapture is a test HTTP server that captures telemetry batch POSTs.
type telemetryCapture struct {
	mu       sync.Mutex
	events   []telemetryEvent
	notifyCh chan struct{}
}

func newTelemetryCapture() *telemetryCapture {
	return &telemetryCapture{notifyCh: make(chan struct{}, 64)}
}

func (tc *telemetryCapture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/telemetry/batches") {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var batch struct {
			Events []telemetryEvent `json:"events"`
		}
		_ = json.Unmarshal(body, &batch)
		tc.mu.Lock()
		tc.events = append(tc.events, batch.Events...)
		tc.mu.Unlock()
		select {
		case tc.notifyCh <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	})
}

func (tc *telemetryCapture) waitFor(t *testing.T, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		tc.mu.Lock()
		got := len(tc.events)
		tc.mu.Unlock()
		if got >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d telemetry events, got %d", count, got)
		}
		select {
		case <-tc.notifyCh:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (tc *telemetryCapture) snapshot() []telemetryEvent {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	cp := make([]telemetryEvent, len(tc.events))
	copy(cp, tc.events)
	return cp
}

func newTestFeedbackIntegration(t *testing.T, endpoint string) *FeedbackIntegration {
	t.Helper()
	fi, err := NewFeedbackIntegration(FeedbackConfig{
		APIKey:   testFeedbackKey(),
		Endpoint: endpoint,
	})
	if err != nil {
		t.Fatalf("NewFeedbackIntegration: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = fi.Close(ctx)
	})
	return fi
}

// TestTelemetryEvent_SessionFields verifies that sessionRef and sessionSource
// are serialized when non-empty and omitted when empty.
func TestTelemetryEvent_SessionFields(t *testing.T) {
	// With session ref — both fields present.
	ev := telemetryEvent{
		InteractionID:  "i-1",
		Surface:        "mcp",
		Operation:      "tools/call/summarize",
		SessionRef:     "run-abc",
		SessionSource:  "mcp",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["sessionRef"] != "run-abc" {
		t.Errorf("sessionRef = %v, want run-abc", m["sessionRef"])
	}
	if m["sessionSource"] != "mcp" {
		t.Errorf("sessionSource = %v, want mcp", m["sessionSource"])
	}

	// Without session ref — both fields omitted.
	ev2 := telemetryEvent{
		InteractionID: "i-2",
		Surface:       "mcp",
		Operation:     "tools/call/get_summary",
	}
	data2, err := json.Marshal(ev2)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m2 map[string]any
	if err := json.Unmarshal(data2, &m2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m2["sessionRef"]; ok {
		t.Error("sessionRef should be omitted when empty")
	}
	if _, ok := m2["sessionSource"]; ok {
		t.Error("sessionSource should be omitted when empty")
	}
}

// TestRecordMCP_SessionCorrelation proves that two operations on the same run
// emit telemetry events with the same sessionRef and sessionSource.
func TestRecordMCP_SessionCorrelation(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)

	// Simulate summarize + get_summary for the same run.
	fi.RecordMCP("i-1", "tools/call/summarize", 0, "run-xyz")
	fi.RecordMCP("i-2", "tools/call/get_summary", 0, "run-xyz")

	tc.waitFor(t, 2, 5*time.Second)

	events := tc.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for _, ev := range events {
		if ev.SessionRef != "run-xyz" {
			t.Errorf("SessionRef = %q, want run-xyz", ev.SessionRef)
		}
		if ev.SessionSource != "mcp" {
			t.Errorf("SessionSource = %q, want mcp", ev.SessionSource)
		}
	}
}

// TestRecordMCP_EmptySessionRef verifies that omitting a session ref produces
// telemetry without sessionRef or sessionSource fields.
func TestRecordMCP_EmptySessionRef(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)

	fi.RecordMCP("i-1", "tools/call/summarize", 0, "")

	tc.waitFor(t, 1, 5*time.Second)

	events := tc.snapshot()
	if len(events) < 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].SessionRef != "" {
		t.Errorf("SessionRef = %q, want empty", events[0].SessionRef)
	}
	if events[0].SessionSource != "" {
		t.Errorf("SessionSource = %q, want empty", events[0].SessionSource)
	}
}

// TestSummarizeTool_CachedRun_SessionRef verifies through the full MCP tool
// path that a cached summarize result emits the cached run ID as sessionRef.
func TestSummarizeTool_CachedRun_SessionRef(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)

	svc, st := newTestService(t)

	// Seed a successful cached run.
	st.CreateRun(&domain.Run{
		ID: "cached-run-1", Status: domain.StatusSucceeded, Stage: domain.StageDone,
		InputType: domain.InputTypeYouTube, SourceURL: "https://youtube.com/watch?v=abc123",
		YouTubeVideoID: "abc123", Engine: domain.EnginePi, Model: "pi-model",
		Format: "summary", Prompt: config.DefaultPrompt,
	})

	raw := NewHandler(svc, "test", fi)
	handler := mcpauth.Middleware(mcpauth.Config{Mode: mcpauth.AuthModeNone})(raw)
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

	// Verify cached response.
	text := textContent(t, result)
	if !strings.Contains(text, "cached") {
		t.Errorf("expected cached response, got: %s", text)
	}

	// Wait for telemetry and verify sessionRef is the cached run ID.
	tc.waitFor(t, 1, 5*time.Second)

	events := tc.snapshot()
	var summarizeEv *telemetryEvent
	for i := range events {
		if events[i].Operation == "tools/call/summarize" {
			summarizeEv = &events[i]
			break
		}
	}
	if summarizeEv == nil {
		t.Fatalf("no tools/call/summarize telemetry event found among %d events", len(events))
	}
	if summarizeEv.SessionRef != "cached-run-1" {
		t.Errorf("SessionRef = %q, want cached-run-1", summarizeEv.SessionRef)
	}
	if summarizeEv.SessionSource != "mcp" {
		t.Errorf("SessionSource = %q, want mcp", summarizeEv.SessionSource)
	}
}

// TestSummarizeTool_NewRunAndGetSummary_SessionRef verifies that a new
// (non-cached) summarize call emits the freshly created run ID as
// sessionRef, and that a subsequent get_summary for the same run emits
// the same sessionRef.
func TestSummarizeTool_NewRunAndGetSummary_SessionRef(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)

	svc, _ := newTestService(t)

	raw := NewHandler(svc, "test", fi)
	handler := mcpauth.Middleware(mcpauth.Config{Mode: mcpauth.AuthModeNone})(raw)
	session := connectClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Submit a new text summary.
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "some text to summarize"},
	})
	if err != nil {
		t.Fatalf("CallTool summarize failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", textContent(t, result))
	}

	// Extract the run ID from the tool response text.
	summarizeText := textContent(t, result)
	runID := extractRunID(t, summarizeText)

	// Call get_summary for the same run.
	getResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": runID},
	})
	if err != nil {
		t.Fatalf("CallTool get_summary failed: %v", err)
	}
	_ = getResult

	// Wait for both telemetry events.
	tc.waitFor(t, 2, 5*time.Second)

	events := tc.snapshot()

	// Find the summarize and get_summary events.
	var sumEv, getEv *telemetryEvent
	for i := range events {
		switch events[i].Operation {
		case "tools/call/summarize":
			sumEv = &events[i]
		case "tools/call/get_summary":
			getEv = &events[i]
		}
	}
	if sumEv == nil {
		t.Fatalf("missing tools/call/summarize telemetry event")
	}
	if getEv == nil {
		t.Fatalf("missing tools/call/get_summary telemetry event")
	}

	// Both events should carry the same run ID as sessionRef.
	if sumEv.SessionRef != runID {
		t.Errorf("summarize SessionRef = %q, want %q", sumEv.SessionRef, runID)
	}
	if getEv.SessionRef != runID {
		t.Errorf("get_summary SessionRef = %q, want %q", getEv.SessionRef, runID)
	}
	if sumEv.SessionSource != "mcp" {
		t.Errorf("summarize SessionSource = %q, want mcp", sumEv.SessionSource)
	}
	if getEv.SessionSource != "mcp" {
		t.Errorf("get_summary SessionSource = %q, want mcp", getEv.SessionSource)
	}
}

// extractRunID pulls the UUID from text like "Summary request accepted. Run ID: <uuid>."
func extractRunID(t *testing.T, text string) string {
	t.Helper()
	idx := strings.Index(text, "Run ID: ")
	if idx < 0 {
		t.Fatalf("could not find 'Run ID: ' in text: %s", text)
	}
	rest := text[idx+len("Run ID: "):]
	end := strings.IndexAny(rest, ". ")
	if end < 0 {
		t.Fatalf("could not find end of run ID in text: %s", rest)
	}
	return rest[:end]
}

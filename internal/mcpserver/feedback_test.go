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

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/mcpauth"
	"github.com/wayanjimmy/summarize/internal/summary"
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(telemetryBatchResponse{
			Accepted: len(batch.Events),
			Dropped:  0,
		})
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
		InteractionID: "i-1",
		Surface:       "mcp",
		Operation:     "summarize",
		SessionRef:    "run-abc",
		SessionSource: "mcp",
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
		Operation:     "get_summary",
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
	runID := uuid.NewString()
	fi.RecordMCP(uuid.NewString(), "summarize", 0, runID, 200, mcpauth.Principal{ID: "local", Anonymous: true})
	fi.RecordMCP(uuid.NewString(), "get_summary", 0, runID, 200, mcpauth.Principal{ID: "local", Anonymous: true})

	tc.waitFor(t, 2, 5*time.Second)

	events := tc.snapshot()
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for _, ev := range events {
		if ev.SessionRef != runID {
			t.Errorf("SessionRef = %q, want %q", ev.SessionRef, runID)
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

	fi.RecordMCP(uuid.NewString(), "summarize", 0, "", 200, mcpauth.Principal{ID: "local", Anonymous: true})

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

	// Seed a successful cached run with a valid UUID.
	cachedRunID := uuid.NewString()
	st.CreateRun(&domain.Run{
		ID: cachedRunID, Status: domain.StatusSucceeded, Stage: domain.StageDone,
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
		if events[i].Operation == "summarize" {
			summarizeEv = &events[i]
			break
		}
	}
	if summarizeEv == nil {
		t.Fatalf("no summarize telemetry event found among %d events", len(events))
	}
	if summarizeEv.SessionRef != cachedRunID {
		t.Errorf("SessionRef = %q, want %q", summarizeEv.SessionRef, cachedRunID)
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
		case "summarize":
			sumEv = &events[i]
		case "get_summary":
			getEv = &events[i]
		}
	}
	if sumEv == nil {
		t.Fatalf("missing summarize telemetry event")
	}
	if getEv == nil {
		t.Fatalf("missing get_summary telemetry event")
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

// --- Epode Customer → Session → Response prototype scenarios ---

// connectClientWithAuth creates an httptest server from the MCP handler
// wrapped with the given auth config and connects a real MCP client.
// For static auth mode, the client is configured to send the bearer token.
func connectClientWithAuth(t *testing.T, svc *summary.Service, fi *FeedbackIntegration, authCfg mcpauth.Config) *mcp.ClientSession {
	t.Helper()
	raw := NewHandler(svc, "test", fi)
	handler := mcpauth.Middleware(authCfg)(raw)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		DisableStandaloneSSE: true,
	}

	// For static auth, inject the bearer token via a custom HTTP client.
	if authCfg.Mode == mcpauth.AuthModeStatic && authCfg.APIKey != "" {
		transport.HTTPClient = &http.Client{
			Transport: &bearerTransport{
				base:  http.DefaultTransport,
				token: authCfg.APIKey,
			},
		}
	}

	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-client", Version: "v0.0.0"},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}},
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

// bearerTransport wraps an http.RoundTripper to inject an Authorization header.
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

// findEvent returns the first captured event matching the operation, or nil.
func findEvent(events []telemetryEvent, op string) *telemetryEvent {
	for i := range events {
		if events[i].Operation == op {
			return &events[i]
		}
	}
	return nil
}

// Scenario 1: summarize + get_summary for same authenticated customer + same run
// => same Session (sessionRef, sessionSource), plus customer identity fields.
func TestEpode_SameRunSameSession(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	svc, _ := newTestService(t)

	authCfg := mcpauth.Config{Mode: mcpauth.AuthModeStatic, APIKey: "test-key-1"}
	session := connectClientWithAuth(t, svc, fi, authCfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create a summarize run.
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "some text to summarize"},
	})
	if err != nil {
		t.Fatalf("CallTool summarize: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", textContent(t, result))
	}
	runID := extractRunID(t, textContent(t, result))

	// Follow-up get_summary for the same run.
	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": runID},
	})
	if err != nil {
		t.Fatalf("CallTool get_summary: %v", err)
	}

	tc.waitFor(t, 2, 5*time.Second)
	events := tc.snapshot()

	sumEv := findEvent(events, "summarize")
	getEv := findEvent(events, "get_summary")
	if sumEv == nil || getEv == nil {
		t.Fatalf("expected summarize and get_summary events, got %d", len(events))
	}

	// Same session.
	if sumEv.SessionRef != runID || getEv.SessionRef != runID {
		t.Errorf("sessionRef mismatch: summarize=%q get_summary=%q want=%q",
			sumEv.SessionRef, getEv.SessionRef, runID)
	}
	if sumEv.SessionSource != "mcp" || getEv.SessionSource != "mcp" {
		t.Errorf("sessionSource mismatch")
	}

	// Customer identity: authenticated (static mode) → userRef, not anonymousRef.
	if sumEv.UserRef != "mcp-static" {
		t.Errorf("UserRef = %q, want mcp-static", sumEv.UserRef)
	}
	if sumEv.AnonymousRef != "" {
		t.Errorf("AnonymousRef should be empty for authenticated user")
	}

	// Completion statusCode and sequence.
	if sumEv.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", sumEv.StatusCode)
	}
	if sumEv.Sequence == 0 || getEv.Sequence == 0 {
		t.Errorf("Sequence should be non-zero")
	}
	if sumEv.Sequence >= getEv.Sequence {
		t.Errorf("summarize Sequence (%d) should be < get_summary Sequence (%d)",
			sumEv.Sequence, getEv.Sequence)
	}
}

// Scenario 2: different runs => different Sessions.
func TestEpode_DifferentRunDifferentSession(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	svc, _ := newTestService(t)

	session := connectClientWithAuth(t, svc, fi, mcpauth.Config{Mode: mcpauth.AuthModeNone})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// First run.
	r1, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "first text"},
	})
	if err != nil || r1.IsError {
		t.Fatalf("summarize 1: err=%v isError=%v", err, r1.IsError)
	}
	run1 := extractRunID(t, textContent(t, r1))

	// Second run.
	r2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "second text"},
	})
	if err != nil || r2.IsError {
		t.Fatalf("summarize 2: err=%v isError=%v", err, r2.IsError)
	}
	run2 := extractRunID(t, textContent(t, r2))

	if run1 == run2 {
		t.Fatalf("expected different run IDs, got %s twice", run1)
	}

	tc.waitFor(t, 2, 5*time.Second)
	events := tc.snapshot()

	ev1 := findEvent(events, "summarize")
	// Find the second summarize event (findEvent returns the first).
	var ev2 *telemetryEvent
	count := 0
	for i := range events {
		if events[i].Operation == "summarize" {
			count++
			if count == 2 {
				ev2 = &events[i]
			}
		}
	}
	if ev1 == nil || ev2 == nil {
		t.Fatalf("expected 2 summarize events, got %d", count)
	}

	if ev1.SessionRef == ev2.SessionRef {
		t.Errorf("expected different sessionRefs, got %q for both", ev1.SessionRef)
	}
	if ev1.SessionRef != run1 {
		t.Errorf("ev1 SessionRef = %q, want %q", ev1.SessionRef, run1)
	}
	if ev2.SessionRef != run2 {
		t.Errorf("ev2 SessionRef = %q, want %q", ev2.SessionRef, run2)
	}

	// Anonymous mode → anonymousRef, not userRef.
	if ev1.AnonymousRef != "local" {
		t.Errorf("AnonymousRef = %q, want local", ev1.AnonymousRef)
	}
	if ev1.UserRef != "" {
		t.Errorf("UserRef should be empty for anonymous user")
	}
}

// Scenario 3: no proven run => accepted but unlinked (no sessionRef/sessionSource).
func TestEpode_NoProvenRunUnlinked(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)

	// Record with empty sessionRef — simulates a tool call without a proven run.
	fi.RecordMCP(uuid.NewString(), "summarize", 42, "", 200,
		mcpauth.Principal{ID: "local", Anonymous: true})

	tc.waitFor(t, 1, 5*time.Second)
	events := tc.snapshot()

	if len(events) < 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]

	// Accepted (event was delivered) but unlinked.
	if ev.SessionRef != "" {
		t.Errorf("SessionRef should be empty, got %q", ev.SessionRef)
	}
	if ev.SessionSource != "" {
		t.Errorf("SessionSource should be empty, got %q", ev.SessionSource)
	}

	// Customer identity still present.
	if ev.AnonymousRef != "local" {
		t.Errorf("AnonymousRef = %q, want local", ev.AnonymousRef)
	}
	if ev.UserRef != "" {
		t.Errorf("UserRef should be empty for anonymous")
	}

	// Duration and statusCode recorded.
	if ev.DurationMS != 42 {
		t.Errorf("DurationMS = %d, want 42", ev.DurationMS)
	}
	if ev.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", ev.StatusCode)
	}
}

// Scenario 3b: invalid (non-UUID) sessionRef => accepted but unlinked.
// The contract requires a validated product-owned run UUID. A non-UUID
// sessionRef must be treated as unproven and omitted.
func TestEpode_InvalidSessionRefUnlinked(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)

	// Record with a non-UUID sessionRef — must be unlinked.
	fi.RecordMCP(uuid.NewString(), "summarize", 10, "not-a-uuid", 200,
		mcpauth.Principal{ID: "local", Anonymous: true})

	tc.waitFor(t, 1, 5*time.Second)
	events := tc.snapshot()

	if len(events) < 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]

	// Accepted but unlinked — no sessionRef or sessionSource.
	if ev.SessionRef != "" {
		t.Errorf("SessionRef should be empty for invalid UUID, got %q", ev.SessionRef)
	}
	if ev.SessionSource != "" {
		t.Errorf("SessionSource should be empty for invalid UUID, got %q", ev.SessionSource)
	}
}

// Scenario 4: second customer/anonymous installation remains partitioned.
// Two different deployments (different anonymousRef) produce telemetry
// with different customer refs and cannot cross-reference sessions.
func TestEpode_CustomerPartitioning(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)

	// Simulate two different anonymous installations with different principal IDs.
	// In none mode, the deployment-stable ID is the anonymousRef.
	principalA := mcpauth.Principal{ID: "deployment-alpha", Anonymous: true}
	principalB := mcpauth.Principal{ID: "deployment-beta", Anonymous: true}

	// Each installation records telemetry for its own run (valid UUIDs).
	runA := uuid.NewString()
	runB := uuid.NewString()
	fi.RecordMCP(uuid.NewString(), "summarize", 50, runA, 200, principalA)
	fi.RecordMCP(uuid.NewString(), "summarize", 60, runB, 200, principalB)

	tc.waitFor(t, 2, 5*time.Second)
	events := tc.snapshot()

	if len(events) < 2 {
		t.Fatalf("expected ≥2 events, got %d", len(events))
	}

	// Find events by their distinct anonymousRef.
	var evA, evB *telemetryEvent
	for i := range events {
		switch events[i].AnonymousRef {
		case "deployment-alpha":
			evA = &events[i]
		case "deployment-beta":
			evB = &events[i]
		}
	}
	if evA == nil || evB == nil {
		t.Fatalf("missing events for deployment-alpha or deployment-beta")
	}

	// Different customer refs (anonymousRef).
	if evA.AnonymousRef != "deployment-alpha" {
		t.Errorf("evA AnonymousRef = %q, want deployment-alpha", evA.AnonymousRef)
	}
	if evB.AnonymousRef != "deployment-beta" {
		t.Errorf("evB AnonymousRef = %q, want deployment-beta", evB.AnonymousRef)
	}
	if evA.AnonymousRef == evB.AnonymousRef {
		t.Errorf("anonymousRef should differ between installations")
	}

	// No userRef for anonymous.
	if evA.UserRef != "" || evB.UserRef != "" {
		t.Errorf("UserRef should be empty for anonymous installations")
	}

	// Different sessions (no cross-referencing).
	if evA.SessionRef == evB.SessionRef {
		t.Errorf("sessionRef should differ between installations")
	}
	if evA.SessionRef != runA {
		t.Errorf("evA SessionRef = %q, want %q", evA.SessionRef, runA)
	}
	if evB.SessionRef != runB {
		t.Errorf("evB SessionRef = %q, want %q", evB.SessionRef, runB)
	}

	// Now verify store-level partitioning: different owners cannot access
	// each other's runs.
	svc, st := newTestService(t)
	ctx := context.Background()

	// Customer A creates a run.
	submitA, err := svc.Submit(ctx, summary.SubmitRequest{
		Text: "alpha text", OwnerID: "deployment-alpha",
	})
	if err != nil {
		t.Fatalf("submit A: %v", err)
	}

	// Customer B creates a run.
	submitB, err := svc.Submit(ctx, summary.SubmitRequest{
		Text: "beta text", OwnerID: "deployment-beta",
	})
	if err != nil {
		t.Fatalf("submit B: %v", err)
	}

	// Customer B cannot get customer A's run.
	_, err = svc.Get(ctx, submitA.RunID, "deployment-beta")
	if err == nil {
		t.Error("customer B should not access customer A's run")
	}

	// Customer A cannot get customer B's run.
	_, err = svc.Get(ctx, submitB.RunID, "deployment-alpha")
	if err == nil {
		t.Error("customer A should not access customer B's run")
	}

	// Verify the runs have different IDs.
	if submitA.RunID == submitB.RunID {
		t.Errorf("runs should have different IDs")
	}

	_ = st // keep reference
}

// Scenario 5: exact retry reuses interaction UUID/payload.
func TestEpode_RetryReusesPayload(t *testing.T) {
	var mu sync.Mutex
	var payloads [][]byte
	var attemptCount int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/telemetry/batches") {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		payloads = append(payloads, body)
		attemptCount++
		ac := attemptCount
		mu.Unlock()

		if ac < 2 {
			// First attempt: 503 to trigger retry.
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Second attempt: 202 with proper receipt.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		var batch struct {
			Events []telemetryEvent `json:"events"`
		}
		_ = json.Unmarshal(body, &batch)
		_ = json.NewEncoder(w).Encode(telemetryBatchResponse{
			Accepted: len(batch.Events),
			Dropped:  0,
		})
	}))
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	// Use a client with a short timeout so retries happen quickly.
	fi.client = &http.Client{Timeout: 5 * time.Second}

	fi.RecordMCP(uuid.NewString(), "summarize", 100, uuid.NewString(), 200,
		mcpauth.Principal{ID: "user-1", Anonymous: false})

	// Wait for at least 2 attempts (retry + success).
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := attemptCount
		mu.Unlock()
		if got >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for retry, got %d attempts", got)
		}
		time.Sleep(200 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) < 2 {
		t.Fatalf("expected ≥2 payloads, got %d", len(payloads))
	}

	// Both payloads should be identical (exact reuse).
	if string(payloads[0]) != string(payloads[1]) {
		t.Errorf("retry payload differs from original:\n  first:  %s\n  second: %s",
			payloads[0], payloads[1])
	}

	// Verify both payloads contain exactly one event with the same interactionId.
	var batch1, batch2 struct {
		Events []telemetryEvent `json:"events"`
	}
	_ = json.Unmarshal(payloads[0], &batch1)
	_ = json.Unmarshal(payloads[1], &batch2)
	if len(batch1.Events) != 1 || len(batch2.Events) != 1 {
		t.Fatalf("expected 1 event per payload, got %d and %d", len(batch1.Events), len(batch2.Events))
	}
	if batch1.Events[0].InteractionID != batch2.Events[0].InteractionID {
		t.Errorf("interactionId differs between retries: %q vs %q",
			batch1.Events[0].InteractionID, batch2.Events[0].InteractionID)
	}
	if batch1.Events[0].InteractionID == "" {
		t.Error("expected non-empty interactionId in payload")
	}
}

// Scenario 6: Epode unavailable/partial receipt does not change MCP result.
func TestEpode_UnavailableDoesNotAffectMCP(t *testing.T) {
	// Epode server that always returns 500.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	fi.client = &http.Client{Timeout: 2 * time.Second}

	svc, _ := newTestService(t)
	session := connectClientWithAuth(t, svc, fi, mcpauth.Config{Mode: mcpauth.AuthModeNone})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// MCP call should succeed even though Epode is down.
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "text with epode down"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("MCP result should not be affected by Epode outage: %s",
			textContent(t, result))
	}

	// Verify the tool returned a valid run ID.
	runID := extractRunID(t, textContent(t, result))
	if runID == "" {
		t.Fatal("expected non-empty run ID")
	}

	// Also test with partial receipt (202 but accepted < batch size).
	tc := newTelemetryCapture()
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		// Return 202 but with accepted=0, dropped=1 (partial receipt).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(telemetryBatchResponse{
			Accepted: 0,
			Dropped:  len(batch.Events),
		})
	}))
	defer ts2.Close()

	fi2 := newTestFeedbackIntegration(t, ts2.URL)
	fi2.client = &http.Client{Timeout: 2 * time.Second}

	svc2, _ := newTestService(t)
	session2 := connectClientWithAuth(t, svc2, fi2, mcpauth.Config{Mode: mcpauth.AuthModeNone})

	result2, err := session2.CallTool(ctx, &mcp.CallToolParams{
		Name:      "summarize",
		Arguments: map[string]any{"text": "text with partial receipt"},
	})
	if err != nil {
		t.Fatalf("CallTool with partial receipt failed: %v", err)
	}
	if result2.IsError {
		t.Fatalf("MCP result should not be affected by partial receipt: %s",
			textContent(t, result2))
	}
	runID2 := extractRunID(t, textContent(t, result2))
	if runID2 == "" {
		t.Fatal("expected non-empty run ID with partial receipt")
	}
}

func TestEpode_ErrorStatusCodeMapping(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	svc, _ := newTestService(t)
	session := connectClientWithAuth(t, svc, fi, mcpauth.Config{Mode: mcpauth.AuthModeNone})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_summary",
		Arguments: map[string]any{"run_id": uuid.NewString()},
	})
	if err != nil {
		t.Fatalf("CallTool get_summary: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an error result, got: %s", textContent(t, result))
	}

	tc.waitFor(t, 1, 5*time.Second)
	event := findEvent(tc.snapshot(), "get_summary")
	if event == nil {
		t.Fatal("missing get_summary telemetry event")
	}
	if event.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", event.StatusCode)
	}
	if event.Operation != "get_summary" {
		t.Errorf("Operation = %q, want get_summary", event.Operation)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal telemetry event: %v", err)
	}
	serialized := strings.ToLower(string(data))
	if strings.Contains(serialized, "not found") || strings.Contains(serialized, "not_found") {
		t.Errorf("telemetry serialized error details: %s", data)
	}
}

func TestEpode_FreshUUIDPerResponse(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	svc, _ := newTestService(t)
	session := connectClientWithAuth(t, svc, fi, mcpauth.Config{Mode: mcpauth.AuthModeNone})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, text := range []string{"first distinct text", "second distinct text"} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "summarize",
			Arguments: map[string]any{"text": text},
		})
		if err != nil {
			t.Fatalf("CallTool summarize: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", textContent(t, result))
		}
	}

	tc.waitFor(t, 2, 5*time.Second)
	events := tc.snapshot()
	if len(events) != 2 {
		t.Fatalf("got %d telemetry events, want 2", len(events))
	}
	if events[0].InteractionID == events[1].InteractionID {
		t.Errorf("separate responses used the same interaction ID %q", events[0].InteractionID)
	}
	for _, event := range events {
		if _, err := uuid.Parse(event.InteractionID); err != nil {
			t.Errorf("InteractionID %q is not a valid UUID: %v", event.InteractionID, err)
		}
	}
}

func TestEpode_RetryableVsTerminalStatus(t *testing.T) {
	for _, test := range []struct {
		name         string
		status       int
		wantAttempts int
		wait         time.Duration
	}{
		{name: "retryable 503", status: http.StatusServiceUnavailable, wantAttempts: 4, wait: 6 * time.Second},
		{name: "terminal 400", status: http.StatusBadRequest, wantAttempts: 1, wait: time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			attempts := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				attempts++
				mu.Unlock()
				w.WriteHeader(test.status)
			}))
			defer ts.Close()

			fi := newTestFeedbackIntegration(t, ts.URL)
			fi.client = &http.Client{Timeout: 2 * time.Second}
			fi.RecordMCP(uuid.NewString(), "summarize", 1, "", 200, mcpauth.Principal{ID: "local", Anonymous: true})

			deadline := time.Now().Add(test.wait)
			for {
				mu.Lock()
				got := attempts
				mu.Unlock()
				if got >= test.wantAttempts || time.Now().After(deadline) {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if test.status == http.StatusBadRequest {
				time.Sleep(time.Second)
			}
			mu.Lock()
			got := attempts
			mu.Unlock()
			if got != test.wantAttempts {
				t.Errorf("attempts = %d, want %d", got, test.wantAttempts)
			}
		})
	}
}

func TestEpode_BoundedQueueOverflow(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	fi.client = &http.Client{Timeout: 50 * time.Millisecond}
	start := time.Now()
	for i := 0; i < 105; i++ {
		fi.RecordMCP(uuid.NewString(), "summarize", 0, "", 200,
			mcpauth.Principal{ID: "local", Anonymous: true})
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("RecordMCP calls blocked for %s, want under 1s", elapsed)
	}
}

func TestEpode_ShutdownFlush(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	for i := 0; i < 3; i++ {
		fi.RecordMCP(uuid.NewString(), "summarize", int64(i), "", 200,
			mcpauth.Principal{ID: "local", Anonymous: true})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fi.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := len(tc.snapshot()); got != 3 {
		t.Errorf("capture received %d events after Close, want 3", got)
	}
}

func TestEpode_NoSensitiveDataInTelemetry(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	fi.RecordMCP(uuid.NewString(), "summarize", 12, "", 200,
		mcpauth.Principal{ID: "local", Anonymous: true})
	tc.waitFor(t, 1, 5*time.Second)
	event := tc.snapshot()[0]
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal telemetry event: %v", err)
	}
	serialized := string(data)
	for _, sensitive := range []string{"password", "secret", "token", "apiKey", "args", "result", "prompt", "transcript", "credential", "exception"} {
		if strings.Contains(serialized, sensitive) {
			t.Errorf("telemetry contains sensitive substring %q: %s", sensitive, serialized)
		}
	}
	for _, safe := range []string{"interactionId", "surface", "operation", "classification", "occurredAt"} {
		if !strings.Contains(serialized, safe) {
			t.Errorf("telemetry is missing safe field %q: %s", safe, serialized)
		}
	}
	if event.RuntimeHint != "summarize-prototype" || event.RuntimeHintSource != "mcp" {
		t.Errorf("runtime hint = %q/%q, want summarize-prototype/mcp", event.RuntimeHint, event.RuntimeHintSource)
	}
}

func TestEpode_RuntimeHintPresent(t *testing.T) {
	tc := newTelemetryCapture()
	ts := httptest.NewServer(tc.handler())
	defer ts.Close()

	fi := newTestFeedbackIntegration(t, ts.URL)
	fi.RecordMCP(uuid.NewString(), "summarize", 0, "", 200,
		mcpauth.Principal{ID: "local", Anonymous: true})
	tc.waitFor(t, 1, 5*time.Second)
	event := tc.snapshot()[0]
	if event.RuntimeHint != "summarize-prototype" {
		t.Errorf("RuntimeHint = %q, want summarize-prototype", event.RuntimeHint)
	}
	if event.RuntimeHintSource != "mcp" {
		t.Errorf("RuntimeHintSource = %q, want mcp", event.RuntimeHintSource)
	}
}

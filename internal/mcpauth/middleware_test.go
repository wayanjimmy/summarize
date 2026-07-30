package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/wayanjimmy/summarize/internal/config"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/engine"
	"github.com/wayanjimmy/summarize/internal/events"
	"github.com/wayanjimmy/summarize/internal/store"
	"github.com/wayanjimmy/summarize/internal/summary"
)

// --- test helpers ---

type mockPublisher struct{}

func (m *mockPublisher) PublishSummaryRequested(runID string) (*events.SummaryRequested, error) {
	return &events.SummaryRequested{
		EventID:   uuid.NewString(),
		EventType: "summary.requested",
		RunID:     runID,
		CreatedAt: time.Now().UTC(),
	}, nil
}

type testLister struct {
	name   string
	models []string
}

func (l *testLister) Name() string { return l.name }
func (l *testLister) ListModels(context.Context) ([]string, error) {
	return l.models, nil
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newTestService(t *testing.T) (*summary.Service, *store.Store) {
	t.Helper()
	st := newTestStore(t)
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

func principalFromCtx(r *http.Request) (Principal, bool) {
	return PrincipalFromContext(r.Context())
}

// --- none mode tests ---

func TestMiddleware_NoneMode(t *testing.T) {
	mw := Middleware(Config{Mode: AuthModeNone})
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFromCtx(r)
		if !ok {
			t.Fatal("no principal in context")
		}
		if p.ID != DefaultOwnerID {
			t.Errorf("principal ID = %q, want %q", p.ID, DefaultOwnerID)
		}
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if !called {
		t.Fatal("handler not called")
	}
}

func TestMiddleware_NoneModeDefault(t *testing.T) {
	// Empty mode defaults to none
	mw := Middleware(Config{Mode: ""})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFromCtx(r)
		if !ok || p.ID != DefaultOwnerID {
			t.Fatalf("principal = %v, ok = %v", p, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// --- static mode tests ---

func TestMiddleware_StaticMode_ValidKey(t *testing.T) {
	mw := Middleware(Config{Mode: AuthModeStatic, APIKey: "secret-key"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFromCtx(r)
		if !ok {
			t.Fatal("no principal in context")
		}
		if p.ID != "mcp-static" {
			t.Errorf("principal ID = %q, want %q", p.ID, "mcp-static")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestMiddleware_StaticMode_InvalidKey(t *testing.T) {
	mw := Middleware(Config{Mode: AuthModeStatic, APIKey: "secret-key"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for invalid key")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_StaticMode_MissingToken(t *testing.T) {
	mw := Middleware(Config{Mode: AuthModeStatic, APIKey: "secret-key"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for missing token")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// --- oauth mode tests ---

// setupOAuthTest creates a mock JWKS server and returns the JWKS URL,
// a function to create signed JWTs, and the expected issuer.
func setupOAuthTest(t *testing.T) (jwksURL, issuer string, makeToken func(sub string) string) {
	t.Helper()

	// Generate RSA key pair
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	issuer = "https://auth.test.com"

	// Build JWKS document
	nBytes := key.N.Bytes()
	eBytes := []byte{0x01, 0x00, 0x01} // 65537
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": "test-key-1",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(nBytes),
				"e":   base64.RawURLEncoding.EncodeToString(eBytes),
			},
		},
	}
	jwksBytes, _ := json.Marshal(jwks)

	// Serve JWKS
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(jwksBytes)
	}))
	t.Cleanup(jwksServer.Close)

	makeToken = func(sub string) string {
		claims := jwt.MapClaims{
			"iss": issuer,
			"sub": sub,
			"exp": time.Now().Add(1 * time.Hour).Unix(),
			"iat": time.Now().Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "test-key-1"
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatalf("sign token: %v", err)
		}
		return signed
	}

	return jwksServer.URL, issuer, makeToken
}

func TestMiddleware_OAuthMode_ValidToken(t *testing.T) {
	jwksURL, issuer, makeToken := setupOAuthTest(t)

	mw := Middleware(Config{
		Mode: AuthModeOAuth,
		OAuth: OAuthConfig{
			Issuer:  issuer,
			JWKSURL: jwksURL,
		},
	})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFromCtx(r)
		if !ok {
			t.Fatal("no principal in context")
		}
		if p.ID != "user-alice" {
			t.Errorf("principal ID = %q, want %q", p.ID, "user-alice")
		}
		if p.Tenant != issuer {
			t.Errorf("tenant = %q, want %q", p.Tenant, issuer)
		}
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken("user-alice"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestMiddleware_OAuthMode_InvalidToken(t *testing.T) {
	jwksURL, issuer, _ := setupOAuthTest(t)

	mw := Middleware(Config{
		Mode: AuthModeOAuth,
		OAuth: OAuthConfig{
			Issuer:  issuer,
			JWKSURL: jwksURL,
		},
	})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for invalid token")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer invalid.jwt.token")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_OAuthMode_MissingToken(t *testing.T) {
	jwksURL, issuer, _ := setupOAuthTest(t)

	mw := Middleware(Config{
		Mode: AuthModeOAuth,
		OAuth: OAuthConfig{
			Issuer:  issuer,
			JWKSURL: jwksURL,
		},
	})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for missing token")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_OAuthMode_WrongIssuer(t *testing.T) {
	jwksURL, _, makeToken := setupOAuthTest(t)

	mw := Middleware(Config{
		Mode: AuthModeOAuth,
		OAuth: OAuthConfig{
			Issuer:  "https://wrong-issuer.com",
			JWKSURL: jwksURL,
		},
	})

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for wrong issuer")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+makeToken("user-bob"))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_OAuthMode_DifferentPrincipals(t *testing.T) {
	jwksURL, issuer, makeToken := setupOAuthTest(t)

	mw := Middleware(Config{
		Mode: AuthModeOAuth,
		OAuth: OAuthConfig{
			Issuer:  issuer,
			JWKSURL: jwksURL,
		},
	})

	// Two different subjects should produce two different principals
	for _, sub := range []string{"alice", "bob"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+makeToken(sub))

		var gotPrincipal Principal
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := principalFromCtx(r)
			gotPrincipal = p
			w.WriteHeader(http.StatusOK)
		}))
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d for sub %q", rr.Code, sub)
		}
		if gotPrincipal.ID != sub {
			t.Errorf("sub %q: principal ID = %q, want %q", sub, gotPrincipal.ID, sub)
		}
	}
}

// --- owner isolation tests (the key Phase 3 testable outcome) ---

func TestOwnerIsolation_CannotAccessOthersRuns(t *testing.T) {
	svc, st := newTestService(t)

	// Alice creates a run
	aliceResult, err := svc.Submit(context.Background(), summary.SubmitRequest{
		Text:    "alice's content",
		OwnerID: "alice",
	})
	if err != nil {
		t.Fatalf("alice Submit error: %v", err)
	}

	// Bob tries to access Alice's run
	_, err = svc.Get(context.Background(), aliceResult.RunID, "bob")
	if err == nil {
		t.Fatal("bob should not be able to access alice's run")
	}
	// Should get not_found, not the run data
	if err.Error() != "run not found" {
		t.Errorf("error = %q, want %q", err.Error(), "run not found")
	}

	// Alice can access her own run
	_, err = svc.Get(context.Background(), aliceResult.RunID, "alice")
	if err != nil {
		t.Fatalf("alice should be able to access her own run: %v", err)
	}

	// Verify the run exists in the store (it's there, just owner-scoped)
	run, err := st.GetRun(aliceResult.RunID)
	if err != nil {
		t.Fatalf("GetRun error: %v", err)
	}
	if run.OwnerID != "alice" {
		t.Errorf("owner_id = %q, want %q", run.OwnerID, "alice")
	}
}

func TestOwnerIsolation_CannotDedupOntoOthersRuns(t *testing.T) {
	svc, st := newTestService(t)

	// Alice creates a successful run for video "vid123"
	aliceRun := &domain.Run{
		ID:              "alice-run-1",
		Status:          domain.StatusSucceeded,
		Stage:           domain.StageDone,
		InputType:       domain.InputTypeYouTube,
		SourceURL:       "https://youtube.com/watch?v=vid123",
		YouTubeVideoID:  "vid123",
		Engine:          domain.EnginePi,
		Model:           "pi-model",
		Format:          "summary",
		Prompt:          config.DefaultPrompt,
		OwnerID:         "alice",
	}
	if err := st.CreateRun(aliceRun); err != nil {
		t.Fatalf("create alice run: %v", err)
	}

	// Bob submits the same video — should NOT get Alice's cached run
	bobResult, err := svc.Submit(context.Background(), summary.SubmitRequest{
		URL:    "https://youtube.com/watch?v=vid123",
		OwnerID: "bob",
	})
	if err != nil {
		t.Fatalf("bob Submit error: %v", err)
	}
	if bobResult.Cached {
		t.Fatal("bob should not get a cache hit on alice's run")
	}
	if bobResult.RunID == "alice-run-1" {
		t.Fatal("bob should not get alice's run ID")
	}

	// Alice submits the same video — SHOULD get her cached run
	aliceResult2, err := svc.Submit(context.Background(), summary.SubmitRequest{
		URL:    "https://youtube.com/watch?v=vid123",
		OwnerID: "alice",
	})
	if err != nil {
		t.Fatalf("alice Submit error: %v", err)
	}
	if !aliceResult2.Cached {
		t.Fatal("alice should get a cache hit on her own run")
	}
	if aliceResult2.RunID != "alice-run-1" {
		t.Errorf("alice cached run ID = %q, want %q", aliceResult2.RunID, "alice-run-1")
	}
}

func TestOwnerIsolation_InFlightDedupScopedByOwner(t *testing.T) {
	svc, st := newTestService(t)

	// Alice has an in-flight run for video "vid456"
	aliceRun := &domain.Run{
		ID:              "alice-inflight",
		Status:          domain.StatusQueued,
		Stage:           domain.StageQueued,
		InputType:       domain.InputTypeYouTube,
		SourceURL:       "https://youtube.com/watch?v=vid456",
		YouTubeVideoID:  "vid456",
		Engine:          domain.EnginePi,
		Model:           "pi-model",
		Format:          "summary",
		Prompt:          config.DefaultPrompt,
		OwnerID:         "alice",
	}
	if err := st.CreateRun(aliceRun); err != nil {
		t.Fatalf("create alice run: %v", err)
	}

	// Bob submits the same video — should NOT dedup onto Alice's in-flight run
	bobResult, err := svc.Submit(context.Background(), summary.SubmitRequest{
		URL:    "https://youtube.com/watch?v=vid456",
		OwnerID: "bob",
	})
	if err != nil {
		t.Fatalf("bob Submit error: %v", err)
	}
	if bobResult.Cached {
		t.Fatal("bob should not dedup onto alice's in-flight run")
	}
	if bobResult.RunID == "alice-inflight" {
		t.Fatal("bob should not get alice's run ID")
	}

	// Alice submits the same video — SHOULD dedup onto her in-flight run
	aliceResult2, err := svc.Submit(context.Background(), summary.SubmitRequest{
		URL:    "https://youtube.com/watch?v=vid456",
		OwnerID: "alice",
	})
	if err != nil {
		t.Fatalf("alice Submit error: %v", err)
	}
	if !aliceResult2.Cached {
		t.Fatal("alice should dedup onto her own in-flight run")
	}
	if aliceResult2.RunID != "alice-inflight" {
		t.Errorf("alice dedup run ID = %q, want %q", aliceResult2.RunID, "alice-inflight")
	}
}

func TestOwnerIsolation_IdempotencyKeyScopedByOwner(t *testing.T) {
	svc, _ := newTestService(t)

	// Alice submits with an idempotency key
	aliceResult, err := svc.Submit(context.Background(), summary.SubmitRequest{
		Text:           "alice's text",
		OwnerID:        "alice",
		IdempotencyKey: "key-123",
	})
	if err != nil {
		t.Fatalf("alice Submit error: %v", err)
	}
	if aliceResult.Cached {
		t.Fatal("first submission should not be cached")
	}

	// Alice retries with the same key — should get the same run
	aliceRetry, err := svc.Submit(context.Background(), summary.SubmitRequest{
		Text:           "alice's text",
		OwnerID:        "alice",
		IdempotencyKey: "key-123",
	})
	if err != nil {
		t.Fatalf("alice retry error: %v", err)
	}
	if !aliceRetry.Cached {
		t.Fatal("alice retry with same key should be cached")
	}
	if aliceRetry.RunID != aliceResult.RunID {
		t.Errorf("alice retry run ID = %q, want %q", aliceRetry.RunID, aliceResult.RunID)
	}

	// Bob uses the same idempotency key — should NOT get Alice's run
	bobResult, err := svc.Submit(context.Background(), summary.SubmitRequest{
		Text:           "bob's text",
		OwnerID:        "bob",
		IdempotencyKey: "key-123",
	})
	if err != nil {
		t.Fatalf("bob Submit error: %v", err)
	}
	if bobResult.Cached {
		t.Fatal("bob with same key should not get alice's run")
	}
	if bobResult.RunID == aliceResult.RunID {
		t.Fatal("bob should not get alice's run ID")
	}
}

func TestOwnerIsolation_DefaultOwnerWorksForLocal(t *testing.T) {
	svc, _ := newTestService(t)

	// REST-style calls with default "local" owner
	result1, err := svc.Submit(context.Background(), summary.SubmitRequest{
		Text:    "local content",
		OwnerID: "local",
	})
	if err != nil {
		t.Fatalf("local Submit error: %v", err)
	}

	// Can retrieve with "local" owner
	_, err = svc.Get(context.Background(), result1.RunID, "local")
	if err != nil {
		t.Fatalf("local Get error: %v", err)
	}

	// Cannot retrieve with a different owner
	_, err = svc.Get(context.Background(), result1.RunID, "attacker")
	if err == nil {
		t.Fatal("attacker should not be able to access local run")
	}
}

// --- context helper tests ---

func TestPrincipalFromContext_Missing(t *testing.T) {
	_, ok := PrincipalFromContext(nil)
	if ok {
		t.Fatal("should return false for nil context")
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Error("equal strings should match")
	}
	if constantTimeEqual("abc", "abd") {
		t.Error("different strings should not match")
	}
	if constantTimeEqual("abc", "ab") {
		t.Error("different length strings should not match")
	}
}

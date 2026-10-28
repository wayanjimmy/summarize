package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/diag"
	"github.com/cschleiden/go-workflows/workflow"
)

func TestDiagStatsMountedWithBackend(t *testing.T) {
	wfBackend := sqlite.NewSqliteBackend(filepath.Join(t.TempDir(), "workflows.db"))
	t.Cleanup(func() { _ = wfBackend.Close() })

	diagBackend, ok := any(wfBackend).(diag.Backend)
	if !ok {
		t.Fatal("sqlite backend does not implement diag.Backend")
	}

	router := NewRouter(&Handlers{}, diagBackend)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/diag/api/stats", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Exercise the mounted dashboard over HTTP with a real SQLite execution.
	// Prefix mistakes break assets/API calls; replacing read-only diagnostics
	// must not reintroduce mutation endpoints or open a second backend.
	instance, err := client.New(wfBackend).CreateWorkflowInstance(context.Background(), client.WorkflowInstanceOptions{InstanceID: "dashboard:summary/test"}, func(ctx workflow.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	for _, path := range []string{"/diag/", "/diag/app.js", "/diag/style.css", "/diag/api/executions?range=today&timezone=Asia%2FJakarta"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, error %v", path, response.StatusCode, readErr)
		}
		if path == "/diag/api/executions?range=today&timezone=Asia%2FJakarta" {
			var result struct {
				Executions []*diag.WorkflowInstanceRef `json:"executions"`
			}
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Executions) != 1 || result.Executions[0].Instance.InstanceID != instance.InstanceID || result.Executions[0].Instance.ExecutionID != instance.ExecutionID {
				t.Fatalf("dashboard did not use application backend: %s", body)
			}
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(method, server.URL+"/diag/api/executions", nil)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s dashboard returned %d, want 405", method, response.StatusCode)
		}
	}
}

func TestDiagRedirect(t *testing.T) {
	wfBackend := sqlite.NewSqliteBackend(filepath.Join(t.TempDir(), "workflows.db"))
	t.Cleanup(func() { _ = wfBackend.Close() })

	diagBackend, ok := any(wfBackend).(diag.Backend)
	if !ok {
		t.Fatal("sqlite backend does not implement diag.Backend")
	}

	router := NewRouter(&Handlers{}, diagBackend)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/diag", nil))
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusMovedPermanently)
	}
	if got := rr.Header().Get("Location"); got != "/diag/" {
		t.Fatalf("Location = %q, want /diag/", got)
	}
}

func TestDiagNotMountedWithoutBackend(t *testing.T) {
	router := NewRouter(&Handlers{}, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/diag/api/stats", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

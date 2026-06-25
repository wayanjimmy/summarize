package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cschleiden/go-workflows/backend/sqlite"
	"github.com/cschleiden/go-workflows/diag"
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

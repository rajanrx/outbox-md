package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// TestGuardCSRF: a cross-origin state-changing request is rejected (403) before it
// reaches the handler; same-origin and non-browser (no Origin) requests pass.
func TestGuardCSRF(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	post := func(origin string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader("{}"))
		req.Host = "localhost:8181"
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := post("http://evil.example.com"); got != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", got)
	}
	// Same-origin + no-Origin (curl/CLI) must NOT be blocked as CSRF — they reach
	// the handler (which may 4xx for other reasons, but never 403 here).
	if got := post("http://localhost:8181"); got == http.StatusForbidden {
		t.Fatal("same-origin POST wrongly blocked as CSRF (403)")
	}
	if got := post(""); got == http.StatusForbidden {
		t.Fatal("no-Origin POST wrongly blocked as CSRF (403)")
	}

	// A safe method is never CSRF-blocked even cross-origin.
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Host = "localhost:8181"
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatal("cross-origin GET wrongly blocked (safe method)")
	}
}

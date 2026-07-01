package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// P1 regression: sources must be enforced at SERVE time, not only at import.
// A doc left in the DB from a broader earlier run must not reappear once the
// whitelist is narrowed.
func TestServeRespectsSourcesWhitelist(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	svc.SetConfig(config.Config{Sources: []string{"docs/specs"}})
	h := NewAPI(svc, s, sse.NewHub())

	inDoc, _, _ := s.CreateDocument("docs/specs/in.md", "a", "import")
	outDoc, _, _ := s.CreateDocument("other/out.md", "b", "import")

	// /api/docs lists only the whitelisted doc.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 || docs[0].ID != inDoc.ID {
		t.Fatalf("/api/docs = %+v, want only docs/specs/in.md", docs)
	}

	// Direct access to the out-of-whitelist doc is hidden (404), but preserved.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+outDoc.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET out-of-source doc = %d, want 404", rec.Code)
	}

	// The whitelisted doc is still reachable.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+inDoc.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET in-source doc = %d, want 200", rec.Code)
	}
}

// Empty/absent sources serves everything (backward-compatible).
func TestServeEmptySourcesServesAll(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	s.CreateDocument("docs/specs/in.md", "a", "import")
	s.CreateDocument("other/out.md", "b", "import")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 2 {
		t.Fatalf("/api/docs = %d docs, want 2 (no whitelist)", len(docs))
	}
}

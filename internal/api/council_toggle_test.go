package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// TestCommentCouncilEndpointAndDocView covers POST /api/comments/{id}/council
// flipping the flag, and the doc view carrying councilEnabled per comment.
func TestCommentCouncilEndpointAndDocView(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, err := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	if err != nil {
		t.Fatal(err)
	}

	// Default (council_default false) → doc view shows councilEnabled=false.
	if got := docViewComment(t, h, doc.ID, c.ID); got.CouncilEnabled {
		t.Fatalf("fresh comment councilEnabled=true in doc view, want false")
	}

	// POST enable → 200 and the flag flips.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/comments/"+c.ID+"/council", strings.NewReader(`{"enabled": true}`)))
	if rec.Code != 200 {
		t.Fatalf("POST council enable: %d %s", rec.Code, rec.Body.String())
	}
	if got := docViewComment(t, h, doc.ID, c.ID); !got.CouncilEnabled {
		t.Fatalf("after enable, doc view councilEnabled=false, want true")
	}

	// POST disable → flips back.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/comments/"+c.ID+"/council", strings.NewReader(`{"enabled": false}`)))
	if rec.Code != 200 {
		t.Fatalf("POST council disable: %d %s", rec.Code, rec.Body.String())
	}
	if got := docViewComment(t, h, doc.ID, c.ID); got.CouncilEnabled {
		t.Fatalf("after disable, doc view councilEnabled=true, want false")
	}

	// Unknown comment → 400 (bad request, like resolve on a missing comment).
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/comments/nope/council", strings.NewReader(`{"enabled": true}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown comment council toggle: %d, want 400", rec.Code)
	}
}

// docViewComment fetches GET /api/docs/{id} and returns the named comment.
func docViewComment(t *testing.T, h http.Handler, docID, commentID string) domain.Comment {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+docID, nil))
	if rec.Code != 200 {
		t.Fatalf("GET doc: %d %s", rec.Code, rec.Body.String())
	}
	var view struct {
		Comments []domain.Comment `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	for _, c := range view.Comments {
		if c.ID == commentID {
			return c
		}
	}
	t.Fatalf("comment %s not in doc view", commentID)
	return domain.Comment{}
}

// TestConfigCarriesCouncilDefault: /api/config exposes councilDefault from the
// effective config (embedded Config struct).
func TestConfigCarriesCouncilDefault(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	// Default: councilDefault false.
	var got struct {
		CouncilDefault bool `json:"councilDefault"`
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.CouncilDefault {
		t.Fatalf("default /api/config councilDefault=true, want false")
	}

	// After SetConfig(council_default true): flows through.
	cfg := config.Defaults()
	cfg.CouncilDefault = true
	svc.SetConfig(cfg)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.CouncilDefault {
		t.Fatalf("/api/config councilDefault=false after SetConfig(true), want true")
	}
}

// TestSettingsCarriesCouncilDefault: GET /api/settings surfaces council_default
// and PUT persists it to outbox.yaml (round-trips through config.Load).
func TestSettingsCarriesCouncilDefault(t *testing.T) {
	h, dir := singleFolderAPI(t, "auto_reply: false\n")

	// GET: council_default present, default false.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if _, ok := got["council_default"]; !ok {
		t.Fatalf("GET /api/settings missing council_default: %v", got)
	}
	if got["council_default"] != false {
		t.Fatalf("GET council_default = %v, want false", got["council_default"])
	}

	// PUT council_default true → persists and re-loads true.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"council_default": true}`)))
	if rec.Code != 200 {
		t.Fatalf("PUT council_default: %d %s", rec.Code, rec.Body.String())
	}
	if cfg := config.Load(dir); !cfg.CouncilDefault {
		t.Fatalf("council_default did not persist as true: %+v", cfg)
	}
}

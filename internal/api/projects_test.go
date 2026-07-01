package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rajanrx/outbox-md/internal/registry"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// TestProjectsEndpointShape verifies GET /api/projects returns [{name, path}]
// as configured on the service.
func TestProjectsEndpointShape(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjects([]registry.Project{
		{Name: "alpha", Path: "/tmp/alpha"},
		{Name: "beta", Path: "/tmp/beta"},
	})
	h := NewAPI(svc, s, sse.NewHub())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /api/projects: %d %s", rec.Code, rec.Body.String())
	}
	var got []registry.Project
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[0].Path != "/tmp/alpha" || got[1].Name != "beta" {
		t.Fatalf("projects = %+v, want alpha+beta", got)
	}
}

// TestDocsIncludeProject verifies each doc in GET /api/docs carries its project,
// and that single-folder docs (no project) report an empty project — back-compat.
func TestDocsIncludeProject(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	_, _, _ = s.CreateDocumentInProject("alpha", "spec.md", "hi", "human")
	_, _, _ = s.CreateDocument("legacy.md", "hi", "human") // empty project
	h := NewAPI(svc, s, sse.NewHub())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /api/docs: %d", rec.Code)
	}
	var docs []struct {
		Path    string `json:"path"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byPath := map[string]string{}
	for _, d := range docs {
		byPath[d.Path] = d.Project
	}
	if byPath["spec.md"] != "alpha" {
		t.Fatalf("spec.md project = %q, want alpha", byPath["spec.md"])
	}
	if _, ok := byPath["legacy.md"]; !ok || byPath["legacy.md"] != "" {
		t.Fatalf("legacy.md project = %q, want empty (back-compat)", byPath["legacy.md"])
	}
}

// TestProjectsEndpointSingleFolder verifies the single-folder mode reports one
// project with an empty name (the UI hides the switcher at ≤1 project).
func TestProjectsEndpointSingleFolder(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjects([]registry.Project{{Name: "", Path: "/data"}})
	h := NewAPI(svc, s, sse.NewHub())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	var got []registry.Project
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || got[0].Name != "" {
		t.Fatalf("single-folder projects = %+v, want one entry with empty name", got)
	}
}

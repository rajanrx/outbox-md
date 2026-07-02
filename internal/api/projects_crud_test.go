package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rajanrx/outbox-md/internal/registry"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// crudServer builds an API handler backed by a fresh registry file, with the
// registry path wired so the projects CRUD write endpoints are enabled. It returns
// the handler and the registry file path (for on-disk assertions).
func crudServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	s, _ := store.Open(":memory:")
	t.Cleanup(func() { _ = s.Close() })
	file := filepath.Join(t.TempDir(), "projects.json")
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetRegistryPath(file)
	return NewAPI(svc, s, sse.NewHub()), file
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	h.ServeHTTP(rec, r)
	return rec
}

// TestProjectsCRUD exercises the full create/read/update/delete cycle through the
// HTTP API, asserting each write is reflected in the registry file.
func TestProjectsCRUD(t *testing.T) {
	h, file := crudServer(t)
	root := t.TempDir()
	for _, d := range []string{"specs", "rfcs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	name := filepath.Base(root)

	// POST — create a single-agent project.
	body := `{"root":` + jsonStr(root) + `,"docs":["specs"],"members":[{"agent":"claude","model":"opus"}]}`
	rec := do(t, h, http.MethodPost, "/api/projects", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	// The registry file reflects it.
	list, err := registry.List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != name || len(list[0].Members) != 1 ||
		list[0].Members[0].Agent != "claude" || list[0].Members[0].Model != "opus" {
		t.Fatalf("registry after POST = %+v, want claude:opus member", list)
	}

	// GET reflects the new project (members + model exposed).
	rec = do(t, h, http.MethodGet, "/api/projects", "")
	var got []projectResp
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got) != 1 || len(got[0].Members) != 1 || got[0].Members[0].Model != "opus" {
		t.Fatalf("GET after POST = %+v, want the created project", got)
	}

	// PATCH — promote to a council (2 members + chair), edit models and docs.
	patch := `{"docs":["specs","rfcs"],"members":[{"agent":"claude","model":"sonnet"},{"agent":"codex","model":"o3"}],"chair":{"agent":"copilot"}}`
	rec = do(t, h, http.MethodPatch, "/api/projects/"+name, patch)
	if rec.Code != 200 {
		t.Fatalf("PATCH: %d %s", rec.Code, rec.Body.String())
	}
	list, _ = registry.List(file)
	if len(list) != 1 || !list[0].IsCouncil() || len(list[0].Docs) != 2 ||
		list[0].Members[0].Model != "sonnet" || list[0].Members[1].Agent != "codex" ||
		list[0].ChairCmd() != "copilot -p {prompt}" {
		t.Fatalf("registry after PATCH = %+v, want council w/ edited models", list)
	}

	// DELETE removes it.
	rec = do(t, h, http.MethodDelete, "/api/projects/"+name, "")
	if rec.Code != 200 {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	if list, _ = registry.List(file); len(list) != 0 {
		t.Fatalf("registry after DELETE = %+v, want empty", list)
	}
}

// TestPostProjectRejectsCouncilWithoutChair verifies a ≥2-member POST with no chair
// is a 400 (validation), not a 500.
func TestPostProjectRejectsCouncilWithoutChair(t *testing.T) {
	h, file := crudServer(t)
	root := t.TempDir()
	body := `{"root":` + jsonStr(root) + `,"docs":["."],"members":[{"agent":"claude"},{"agent":"codex"}]}`
	rec := do(t, h, http.MethodPost, "/api/projects", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST council-no-chair: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if list, _ := registry.List(file); len(list) != 0 {
		t.Fatalf("a rejected POST must register nothing, got %+v", list)
	}
}

// TestPostProjectRejectsBadRoot verifies a POST with a non-existent root is a 400.
func TestPostProjectRejectsBadRoot(t *testing.T) {
	h, _ := crudServer(t)
	body := `{"root":"/no/such/dir/really","docs":["."],"members":[{"agent":"claude"}]}`
	rec := do(t, h, http.MethodPost, "/api/projects", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST bad root: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestPatchUnknownProjectIs404 verifies editing a missing project is a 404.
func TestPatchUnknownProjectIs404(t *testing.T) {
	h, _ := crudServer(t)
	rec := do(t, h, http.MethodPatch, "/api/projects/ghost", `{"docs":["."]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH ghost: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// TestDeleteUnknownProjectIs404 verifies deleting a missing project is a 404.
func TestDeleteUnknownProjectIs404(t *testing.T) {
	h, _ := crudServer(t)
	rec := do(t, h, http.MethodDelete, "/api/projects/ghost", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE ghost: %d %s, want 404", rec.Code, rec.Body.String())
	}
}

// TestProjectsCRUDRejectedInSingleFolderMode verifies the write endpoints are 409
// when the server is not registry-backed (no registry path set).
func TestProjectsCRUDRejectedInSingleFolderMode(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil }) // no SetRegistryPath
	h := NewAPI(svc, s, sse.NewHub())
	rec := do(t, h, http.MethodPost, "/api/projects", `{"root":"/x","docs":["."]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST without registry: %d, want 409", rec.Code)
	}
}

// jsonStr quotes s as a JSON string literal (for embedding a filesystem path with
// possible special characters into a request body).
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

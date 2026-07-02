package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/registry"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// TestConfigEndpointIncludesVersion verifies /api/config carries the build
// version alongside the config fields (defaults to "dev", overridable).
func TestConfigEndpointIncludesVersion(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	h := NewAPI(svc, s, sse.NewHub())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	var got struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "dev" {
		t.Fatalf("default version = %q, want dev", got.Version)
	}

	svc.SetVersion("1.2.3")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	_ = json.Unmarshal(rr2.Body.Bytes(), &got)
	if got.Version != "1.2.3" {
		t.Fatalf("version after SetVersion = %q, want 1.2.3", got.Version)
	}
}

// singleFolderAPI wires an API over a temp dir seeded with the given outbox.yaml
// content, in single-folder mode (one project keyed "").
func singleFolderAPI(t *testing.T, yaml string) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(dir, "outbox.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := store.Open(":memory:")
	t.Cleanup(func() { s.Close() })
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjects([]registry.Project{{Name: "", Root: dir, Docs: []string{"."}}})
	return NewAPI(svc, s, sse.NewHub()), dir
}

// TestSettingsGetAndPutSingleFolder covers the happy path: GET returns the
// current values, PUT persists valid ones AND preserves comments / unmanaged keys.
func TestSettingsGetAndPutSingleFolder(t *testing.T) {
	seed := "# starter guidance\nsources:\n  - docs/specs\nauto_reply: false\n"
	h, dir := singleFolderAPI(t, seed)
	path := filepath.Join(dir, "outbox.yaml")

	// GET reflects the seeded auto_reply=false and the default agent_cmd.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rr.Code != 200 {
		t.Fatalf("GET settings: %d %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["auto_reply"] != false {
		t.Fatalf("GET auto_reply = %v, want false", got["auto_reply"])
	}

	// PUT flips auto_reply and sets a custom agent_cmd (with metacharacters that
	// must be YAML-quoted correctly).
	body := `{"auto_reply": true, "agent_cmd": "claude -p {prompt} --allowedTools *"}`
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
	if rr.Code != 200 {
		t.Fatalf("PUT settings: %d %s", rr.Code, rr.Body.String())
	}

	// The file must reflect the new values AND keep the comment + unmanaged sources.
	raw, _ := os.ReadFile(path)
	txt := string(raw)
	if !strings.Contains(txt, "auto_reply: true") {
		t.Fatalf("auto_reply not written:\n%s", txt)
	}
	if !strings.Contains(txt, "starter guidance") || !strings.Contains(txt, "docs/specs") {
		t.Fatalf("comment / unmanaged sources clobbered:\n%s", txt)
	}
	// agent_cmd must re-parse to exactly the value we wrote (round-trip / quoting).
	cfg := config.Load(dir)
	if cfg.AgentCmd != "claude -p {prompt} --allowedTools *" {
		t.Fatalf("agent_cmd round-trip = %q", cfg.AgentCmd)
	}
	if !cfg.AutoReply {
		t.Fatalf("auto_reply did not persist as true: %+v", cfg)
	}
}

// TestSettingsRejectsBadInput covers the validation paths: unknown key, wrong
// value type, and unknown project.
func TestSettingsRejectsBadInput(t *testing.T) {
	h, _ := singleFolderAPI(t, "auto_reply: false\n")

	cases := []struct {
		name, target, body string
		wantCode           int
	}{
		{"unknown key", "/api/settings", `{"bogus": true}`, http.StatusBadRequest},
		{"wrong type", "/api/settings", `{"auto_reply": "yes please"}`, http.StatusBadRequest},
		{"malformed json", "/api/settings", `{`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, c.target, strings.NewReader(c.body)))
			if rr.Code != c.wantCode {
				t.Fatalf("%s: code = %d want %d (%s)", c.name, rr.Code, c.wantCode, rr.Body.String())
			}
		})
	}
}

// TestSettingsUnknownProjectMultiMode verifies an unknown ?project rejects in
// multi-project mode (single-folder mode ignores the param and always resolves).
func TestSettingsUnknownProjectMultiMode(t *testing.T) {
	dirA := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "outbox.yaml"), []byte("auto_reply: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirB, "outbox.yaml"), []byte("auto_reply: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetProjects([]registry.Project{
		{Name: "alpha", Root: dirA, Docs: []string{"."}},
		{Name: "beta", Root: dirB, Docs: []string{"."}},
	})
	h := NewAPI(svc, s, sse.NewHub())

	// Unknown project → 404.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/settings?project=ghost", strings.NewReader(`{"auto_reply": true}`)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project code = %d want 404 (%s)", rr.Code, rr.Body.String())
	}

	// A PUT targeting beta writes beta's file, not alpha's.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/settings?project=beta", strings.NewReader(`{"auto_reply": true}`)))
	if rr.Code != 200 {
		t.Fatalf("PUT beta: %d %s", rr.Code, rr.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dirB, "outbox.yaml")); !strings.Contains(string(b), "auto_reply: true") {
		t.Fatalf("beta not updated:\n%s", b)
	}
	if a, _ := os.ReadFile(filepath.Join(dirA, "outbox.yaml")); strings.Contains(string(a), "auto_reply: true") {
		t.Fatalf("alpha wrongly updated:\n%s", a)
	}
}

// TestSettingsCouncilIntFields: GET surfaces the council guardrails at their
// effective (default) values, and PUT accepts an int and persists it.
func TestSettingsCouncilIntFields(t *testing.T) {
	h, dir := singleFolderAPI(t, "auto_reply: false\n")
	path := filepath.Join(dir, "outbox.yaml")

	// GET shows the three council keys at their defaults (2 / 200000 / 50).
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rr.Code != 200 {
		t.Fatalf("GET settings: %d %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	// JSON numbers decode to float64.
	if got["council_rounds"] != float64(2) {
		t.Fatalf("GET council_rounds = %v, want 2", got["council_rounds"])
	}
	if got["council_budget"] != float64(200000) {
		t.Fatalf("GET council_budget = %v, want 200000", got["council_budget"])
	}
	if got["council_deadlock_threshold"] != float64(50) {
		t.Fatalf("GET council_deadlock_threshold = %v, want 50", got["council_deadlock_threshold"])
	}

	// PUT an int value; it persists as a bare int and round-trips.
	body := `{"council_rounds": 4}`
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
	if rr.Code != 200 {
		t.Fatalf("PUT settings: %d %s", rr.Code, rr.Body.String())
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "council_rounds: 4") {
		t.Fatalf("council_rounds not written:\n%s", raw)
	}
	if cfg := config.Load(dir); cfg.ResolveCouncilRounds() != 4 {
		t.Fatalf("council_rounds round-trip = %d, want 4", cfg.ResolveCouncilRounds())
	}

	// A non-int value is rejected.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"council_budget": "lots"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT non-int council_budget code = %d, want 400", rr.Code)
	}
}

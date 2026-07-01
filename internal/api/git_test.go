package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gitsvc "github.com/rajanrx/outbox-md/internal/git"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

func gitFixture(t *testing.T, dir, file, content string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available to build fixture")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
}

func newHandler(t *testing.T, dir string) http.Handler {
	t.Helper()
	s, _ := store.Open(":memory:")
	t.Cleanup(func() { s.Close() })
	svc := service.New(s, func(_, _ string) error { return nil })
	return NewAPI(svc, s, sse.NewHub(), gitsvc.Open(dir))
}

func TestConfigReportsHasGit(t *testing.T) {
	// Non-git served dir → hasGit false.
	plain := t.TempDir()
	rr := httptest.NewRecorder()
	newHandler(t, plain).ServeHTTP(rr, httptest.NewRequest("GET", "/api/config", nil))
	var cfg struct {
		HasGit bool `json:"hasGit"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &cfg)
	if cfg.HasGit {
		t.Fatal("plain dir: hasGit should be false")
	}
	// The embedded config fields must still be present.
	if !contains(rr.Body.String(), "agent") {
		t.Fatalf("config body missing embedded fields: %s", rr.Body.String())
	}

	// Git served dir → hasGit true.
	repo := t.TempDir()
	gitFixture(t, repo, "spec.md", "hello\n")
	rr = httptest.NewRecorder()
	newHandler(t, repo).ServeHTTP(rr, httptest.NewRequest("GET", "/api/config", nil))
	_ = json.Unmarshal(rr.Body.Bytes(), &cfg)
	if !cfg.HasGit {
		t.Fatalf("git dir: hasGit should be true, body %s", rr.Body.String())
	}
}

func TestGitDiffEndpoint(t *testing.T) {
	repo := t.TempDir()
	gitFixture(t, repo, "spec.md", "one\ntwo\nthree\n")
	if err := os.WriteFile(filepath.Join(repo, "spec.md"), []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	newHandler(t, repo).ServeHTTP(rr, httptest.NewRequest("GET", "/api/git/diff", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	var res gitsvc.Result
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Enabled || len(res.Files) != 1 || res.Files[0].Path != "spec.md" {
		t.Fatalf("diff result = %+v", res)
	}
	var sawIns bool
	for _, r := range res.Files[0].Rows {
		if r.Op == "ins" && r.Text == "TWO" {
			sawIns = true
		}
	}
	if !sawIns {
		t.Fatalf("expected ins row, got %+v", res.Files[0].Rows)
	}
}

func TestGitDiffEndpointNonGit(t *testing.T) {
	rr := httptest.NewRecorder()
	newHandler(t, t.TempDir()).ServeHTTP(rr, httptest.NewRequest("GET", "/api/git/diff", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	var res gitsvc.Result
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	if res.Enabled {
		t.Fatal("non-git served dir should report enabled:false")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

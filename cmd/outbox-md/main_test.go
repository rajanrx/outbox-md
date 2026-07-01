package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/store"
)

// seedTree lays out a fixed set of .md files under dir for the ingest tests.
func seedTree(t *testing.T, dir string) {
	t.Helper()
	for _, rel := range []string{"a/a.md", "b/b.md", "c/c.md", "b/nested/n.md"} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("# "+rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func importedPaths(t *testing.T, st *store.Store) []string {
	t.Helper()
	docs, err := st.ListDocuments()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range docs {
		out = append(out, d.Path)
	}
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestImportMarkdownEmptySourcesWalksAll(t *testing.T) {
	dir := t.TempDir()
	seedTree(t, dir)
	st, _ := store.Open(":memory:")
	defer st.Close()
	if err := importMarkdown(st, dir, nil); err != nil {
		t.Fatal(err)
	}
	got := importedPaths(t, st)
	want := []string{"a/a.md", "b/b.md", "b/nested/n.md", "c/c.md"}
	if !eq(got, want) {
		t.Fatalf("empty sources imported %v, want all %v", got, want)
	}
}

func TestImportMarkdownWhitelistFolders(t *testing.T) {
	dir := t.TempDir()
	seedTree(t, dir)
	st, _ := store.Open(":memory:")
	defer st.Close()
	// Whitelist folder "a" (recursive) and glob "b/*.md" (non-recursive) — "c" and
	// the nested b/nested/n.md must NOT be imported.
	if err := importMarkdown(st, dir, []string{"a", "b/*.md"}); err != nil {
		t.Fatal(err)
	}
	got := importedPaths(t, st)
	want := []string{"a/a.md", "b/b.md"}
	if !eq(got, want) {
		t.Fatalf("whitelist imported %v, want %v (c excluded, nested excluded by non-recursive glob)", got, want)
	}
}

// P2: a glob that matches a directory must NOT be recursed at import (single-
// level, mirroring config.Config.Serves) — otherwise a nested file gets imported
// and then hidden at serve time, an import/serve mismatch.
func TestImportMarkdownGlobDoesNotRecurseMatchedDirs(t *testing.T) {
	dir := t.TempDir()
	seedTree(t, dir)
	st, _ := store.Open(":memory:")
	defer st.Close()
	// "b/*" matches b/b.md (file) and b/nested (dir); the matched dir is skipped.
	if err := importMarkdown(st, dir, []string{"b/*"}); err != nil {
		t.Fatal(err)
	}
	if got, want := importedPaths(t, st), []string{"b/b.md"}; !eq(got, want) {
		t.Fatalf("glob-matched dir was recursed: imported %v, want %v", got, want)
	}
	// The imported set must equal what Serves would allow (no import/serve drift).
	cfg := config.Config{Sources: []string{"b/*"}}
	if !cfg.Serves("b/b.md") || cfg.Serves("b/nested/n.md") {
		t.Fatalf("import/serve drift: Serves b/b.md=%v (want true), b/nested/n.md=%v (want false)",
			cfg.Serves("b/b.md"), cfg.Serves("b/nested/n.md"))
	}
}

func TestImportMarkdownRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	seedTree(t, dir)
	st, _ := store.Open(":memory:")
	defer st.Close()
	if err := importMarkdown(st, dir, []string{"../escape"}); err == nil {
		t.Fatal("expected error for a source escaping OUTBOX_DIR")
	}
}

func TestAtomicWritePreservesModeAndContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(p, "new content"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "new content" {
		t.Fatalf("content = %q", b)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644 (rename must not change permissions)", fi.Mode().Perm())
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	if _, err := safeJoin("/data", "../etc/passwd"); err == nil {
		t.Fatal("expected traversal rejection for ../etc/passwd")
	}
	if _, err := safeJoin("/data", "nested/../../escape"); err == nil {
		t.Fatal("expected traversal rejection for nested escape")
	}
	if got, err := safeJoin("/data", "spec.md"); err != nil || got != "/data/spec.md" {
		t.Fatalf("safeJoin(spec.md) = %q, %v", got, err)
	}
}

func TestEnsureDataDirRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureDataDir(f); err == nil {
		t.Fatal("expected error when the data path is a file, not a directory")
	}
	d := filepath.Join(t.TempDir(), "created")
	if err := ensureDataDir(d); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		t.Fatal("ensureDataDir should create a missing directory")
	}
}

// TestResolveCmdRouting table-tests the arg→subcommand routing without touching
// a live listener. Bare invocation and an explicit "serve" both select "serve"
// (the Docker ENTRYPOINT relies on the bare case).
func TestResolveCmdRouting(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantName string
		wantRest []string
	}{
		{"bare selects serve", nil, "serve", nil},
		{"explicit serve", []string{"serve"}, "serve", []string{}},
		{"serve with flags", []string{"serve", "-dir", "x"}, "serve", []string{"-dir", "x"}},
		{"up", []string{"up"}, "up", []string{}},
		{"init", []string{"init"}, "init", []string{}},
		{"version", []string{"version"}, "version", []string{}},
		{"unknown passes through", []string{"bogus"}, "bogus", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotRest := resolveCmd(tc.args)
			if gotName != tc.wantName {
				t.Fatalf("name = %q, want %q", gotName, tc.wantName)
			}
			if !eq(gotRest, tc.wantRest) {
				t.Fatalf("rest = %v, want %v", gotRest, tc.wantRest)
			}
		})
	}
}

func TestRunVersionPrints(t *testing.T) {
	old := version
	version = "1.2.3"
	defer func() { version = old }()
	var buf bytes.Buffer
	if err := run([]string{"version"}, &buf); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "1.2.3" {
		t.Fatalf("version output = %q, want %q", got, "1.2.3")
	}
}

func TestRunUnknownCommandErrorsWithUsage(t *testing.T) {
	var buf bytes.Buffer
	err := run([]string{"bogus"}, &buf)
	if err == nil {
		t.Fatal("unknown command should return a non-nil error (non-zero exit)")
	}
	if !strings.Contains(buf.String(), "Usage:") {
		t.Fatalf("unknown command should print usage, got:\n%s", buf.String())
	}
}

func TestRunHelpPrintsUsage(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		var buf bytes.Buffer
		if err := run([]string{arg}, &buf); err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		if !strings.Contains(buf.String(), "Usage:") {
			t.Fatalf("%s: expected usage output, got:\n%s", arg, buf.String())
		}
	}
}

// TestInitWritesConfig covers init writing a fresh outbox.yaml. lookPath is
// stubbed so `claude` reads as absent and the test never shells out to a real
// binary; init must still succeed and degrade to printing the manual command.
func TestInitWritesConfig(t *testing.T) {
	dir := t.TempDir()
	restore := stubClaudeAbsent(t)
	defer restore()

	var buf bytes.Buffer
	if err := run([]string{"init", "-dir", dir}, &buf); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "outbox.yaml")
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("init did not write outbox.yaml: %v", err)
	}
	if !strings.Contains(string(b), "sources:") {
		t.Fatalf("starter outbox.yaml missing commented sources example:\n%s", b)
	}
	out := buf.String()
	if !strings.Contains(out, "wrote "+cfg) {
		t.Fatalf("expected 'wrote %s' in output, got:\n%s", cfg, out)
	}
	// claude absent → the exact registration command must be printed, not run.
	if !strings.Contains(out, "claude mcp add --transport http outbox-md http://localhost:8181/mcp") {
		t.Fatalf("expected manual MCP command in output, got:\n%s", out)
	}
}

// TestInitKeepsExistingConfig verifies init never overwrites an existing file.
func TestInitKeepsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	restore := stubClaudeAbsent(t)
	defer restore()

	cfg := filepath.Join(dir, "outbox.yaml")
	if err := os.WriteFile(cfg, []byte("sources:\n  - mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := run([]string{"init", "-dir", dir}, &buf); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	if string(b) != "sources:\n  - mine\n" {
		t.Fatalf("init overwrote an existing outbox.yaml: %q", b)
	}
	if !strings.Contains(buf.String(), "kept existing "+cfg) {
		t.Fatalf("expected 'kept existing' message, got:\n%s", buf.String())
	}
}

// stubClaudeAbsent forces lookPath to report `claude` as not installed so init
// tests exercise the graceful-degradation path without a real CLI.
func stubClaudeAbsent(t *testing.T) (restore func()) {
	t.Helper()
	orig := lookPath
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	return func() { lookPath = orig }
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q, want \"ok\"", rec.Body.String())
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/registry"
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

// writeFileTree writes each rel→content under dir, creating parent folders.
func writeFileTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// sortedDocPaths returns the sorted Path of every doc /api/docs serves through h
// — the SERVED set, which for a correctly-wired server equals the imported set.
func sortedDocPaths(t *testing.T, h http.Handler) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatalf("decode /api/docs: %v (body=%s)", err, rec.Body.String())
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
	if err := importMarkdown(st, "", dir, dir, nil); err != nil {
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
	if err := importMarkdown(st, "", dir, dir, []string{"a", "b/*.md"}); err != nil {
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
	if err := importMarkdown(st, "", dir, dir, []string{"b/*"}); err != nil {
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

// Sources are now match-only patterns evaluated against ROOT-RELATIVE keys
// (never joined onto the filesystem), so a pattern that "escapes" simply matches
// no walked key: it imports nothing and can never read outside the walk root.
// The former join-time traversal-rejection error no longer applies.
func TestImportMarkdownEscapingSourceMatchesNothing(t *testing.T) {
	dir := t.TempDir()
	seedTree(t, dir)
	st, _ := store.Open(":memory:")
	defer st.Close()
	if err := importMarkdown(st, "", dir, dir, []string{"../escape"}); err != nil {
		t.Fatalf("escaping source should be a no-op, got error: %v", err)
	}
	if got := importedPaths(t, st); len(got) != 0 {
		t.Fatalf("escaping source imported %v, want nothing", got)
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

// TestUnknownCommandErrors verifies an unknown subcommand prints the top-level
// help and returns a non-nil error (bare invocation is covered separately by
// TestBareInvocationPrintsHelp, which asserts help rather than a server start).
func TestUnknownCommandErrors(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"bogus"}, &out); err == nil {
		t.Fatal("unknown command should error")
	}
	if !strings.Contains(out.String(), "Commands") {
		t.Fatalf("unknown command should print top-level help:\n%s", out.String())
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

// TestInitWritesConfig covers init writing a fresh outbox.yaml with no AI client
// installed. lookPath is stubbed so every command probe reads absent and the
// test never shells out to a real binary; a temp HOME hides any real client
// config dirs. init must still succeed, scaffold the yaml, write no client
// config, and report each client as not detected — exit 0.
func TestInitWritesConfig(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
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
	// Nothing installed → every client reported as not detected, none wired.
	if !strings.Contains(out, "not detected") {
		t.Fatalf("expected 'not detected' summary, got:\n%s", out)
	}
	if strings.Contains(out, "registered (") {
		t.Fatalf("no client should be wired when none is installed, got:\n%s", out)
	}
}

// TestInitAllPrintsClaudeCommand verifies -all attempts every client even when
// none is installed: file clients get configs under the temp HOME, and Claude
// Code (no config file) degrades to printing the exact manual command.
func TestInitAllPrintsClaudeCommand(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	restore := stubClaudeAbsent(t)
	defer restore()

	var buf bytes.Buffer
	if err := run([]string{"init", "-dir", dir, "-all"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "claude mcp add --transport http outbox-md http://localhost:8181/mcp") {
		t.Fatalf("expected manual Claude command under -all, got:\n%s", out)
	}
	// A file client (Cursor) must have been written under the temp HOME.
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err != nil {
		t.Fatalf("-all did not write cursor config: %v", err)
	}
}

// TestInitKeepsExistingConfig verifies init never overwrites an existing file.
func TestInitKeepsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
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

// TestBuildServerMultiWiresPerProjectSourcesGuard is the wiring test: it proves
// buildServer actually connects the per-project sources guard, which the
// api/mcp package tests (which set the map by hand) cannot. It reproduces PR #42
// P2's runtime state — a doc that entered the shared store under a broad config,
// then a project whose outbox.yaml narrows sources so that doc is excluded — and
// asserts the doc is hidden on the handler buildServer returns. If buildServer
// stops calling SetProjectSources (the original bug), the guard reverts to the
// nil-fallback on the global cfg, whose Sources is nil in multi mode, so the
// excluded doc would reappear and this test fails.
func TestBuildServerMultiWiresPerProjectSourcesGuard(t *testing.T) {
	// Redirect the multi-mode shared DB (configHomeDir/outbox.db) into a temp dir.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	projDir := t.TempDir()
	// The project narrows sources to docs/specs; only served files live there.
	if err := os.WriteFile(filepath.Join(projDir, "outbox.yaml"),
		[]byte("sources:\n  - docs/specs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	served := filepath.Join(projDir, "docs", "specs", "in.md")
	if err := os.MkdirAll(filepath.Dir(served), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(served, []byte("# in\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-seed the shared multi-mode DB with a doc OUTSIDE the narrowed whitelist,
	// as if a broader earlier run had imported it. Close it before buildServer
	// opens the same file so the single-connection store never contends.
	dbDir := filepath.Join(home, "outbox")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed, err := store.Open("file:" + filepath.Join(dbDir, "outbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := seed.CreateDocumentInProject("proj", "secret.md", "top secret", "import")
	if err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()

	// A single named project forces multi mode (DB at configHomeDir).
	h, stop, err := buildServer(projDir, []registry.Project{{Name: "proj", Root: projDir, Docs: []string{"."}}}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// The narrowed-out, previously-imported doc must be absent from /api/docs.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	var docs []domain.Document
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	for _, d := range docs {
		if d.ID == secret.ID {
			t.Fatalf("/api/docs leaked the narrowed-out doc secret.md: %+v", docs)
		}
	}
	// And a direct read is a 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+secret.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET narrowed-out doc via buildServer = %d, want 404", rec.Code)
	}
	// Sanity: the served file was imported and IS visible (guard isn't hiding all).
	if len(docs) != 1 || docs[0].Path != "docs/specs/in.md" {
		t.Fatalf("/api/docs = %+v, want only docs/specs/in.md", docs)
	}
}

// P2 (import drift): `add /repo docs/specs` with /repo/outbox.yaml carrying
// `sources: [docs/specs]` must import /repo/docs/specs/*.md (root-relative
// sources match — NO double path like docs/specs/docs/specs) and serve exactly
// that set. A file under a DIFFERENT subtree (not in the docs list) is excluded.
// The imported set must equal the served set — no import/serve drift.
func TestBuildServerImportSourcesRootRelativeNoDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	projDir := t.TempDir()
	// outbox.yaml sources is root-relative and equals the docs subpath — the exact
	// shape that used to double-join to docs/specs/docs/specs and import nothing.
	if err := os.WriteFile(filepath.Join(projDir, "outbox.yaml"),
		[]byte("sources:\n  - docs/specs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Two files under the served subtree (one nested) + one under a different
	// subtree that is NOT in the docs list.
	writeFileTree(t, projDir, map[string]string{
		"docs/specs/a.md":        "# a\n",
		"docs/specs/nested/c.md": "# c\n",
		"other/b.md":             "# b\n",
	})

	// add /repo docs/specs → Docs:["docs/specs"].
	h, stop, err := buildServer(projDir, []registry.Project{
		{Name: "proj", Root: projDir, Docs: []string{"docs/specs"}},
	}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	want := []string{"docs/specs/a.md", "docs/specs/nested/c.md"}

	// Imported set: exactly the docs/specs subtree, no double-path, no other/b.md.
	got := sortedDocPaths(t, h)
	if !eq(got, want) {
		t.Fatalf("served/import set = %v, want %v (root-relative sources, no double path, no other subtree)", got, want)
	}
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

// --- auto-reply flag + wiring ---

func TestResolveFlagsAutoReply(t *testing.T) {
	var out bytes.Buffer
	// Default: absent flag → false.
	_, _, ar, _, err := resolveFlags("serve", nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if ar {
		t.Fatal("auto-reply should default false when the flag is absent")
	}
	// Present: -auto-reply → true.
	_, _, ar, _, err = resolveFlags("serve", []string{"-auto-reply"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !ar {
		t.Fatal("-auto-reply should flip auto-reply on")
	}
}

func TestAutoReplyNotifierOffByDefault(t *testing.T) {
	// Neither flag nor config → no engine wired.
	if n := autoReplyNotifier(t.TempDir(), nil, config.Config{}, false, true, nil); n != nil {
		t.Fatal("autoReplyNotifier should be nil when off (no flag, no config)")
	}
}

func TestAutoReplyNotifierFlagForcesOn(t *testing.T) {
	n := autoReplyNotifier(t.TempDir(), nil, config.Config{AutoReply: false}, true, true, nil)
	if n == nil {
		t.Fatal("the -auto-reply flag should force an engine even when config is false")
	}
	if !n.Enabled() {
		t.Fatal("wired engine should report Enabled() true")
	}
}

func TestAutoReplyNotifierConfigEnables(t *testing.T) {
	n := autoReplyNotifier(t.TempDir(), nil, config.Config{AutoReply: true}, false, true, nil)
	if n == nil {
		t.Fatal("auto_reply: true in config should wire an engine without the flag")
	}
	if !n.Enabled() {
		t.Fatal("wired engine should report Enabled() true")
	}
}

// TestAddAndListProjects covers `outbox add <root> [docs] --agent <preset>`
// registering a project, and both `outbox list` and its `outbox projects` alias
// printing the entry (name → root/docs [agent]).
func TestAddAndListProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "specs"), 0o755); err != nil {
		t.Fatal(err)
	}

	// add with a docs subpath and a codex preset.
	var addOut bytes.Buffer
	if err := run([]string{"add", root, "docs/specs", "--agent", "codex"}, &addOut); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(addOut.String(), "docs/specs") || !strings.Contains(addOut.String(), "codex exec {prompt}") {
		t.Fatalf("add output missing docs/agent:\n%s", addOut.String())
	}

	wantName := filepath.Base(root)
	// `list` prints the entry.
	var listOut bytes.Buffer
	if err := run([]string{"list"}, &listOut); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut.String(), wantName) ||
		!strings.Contains(listOut.String(), "docs/specs") ||
		!strings.Contains(listOut.String(), "codex exec {prompt}") {
		t.Fatalf("list output missing entry fields:\n%s", listOut.String())
	}

	// `projects` is an alias: identical output.
	var projOut bytes.Buffer
	if err := run([]string{"projects"}, &projOut); err != nil {
		t.Fatalf("projects: %v", err)
	}
	if projOut.String() != listOut.String() {
		t.Fatalf("projects alias output %q != list output %q", projOut.String(), listOut.String())
	}
}

// TestAddRejectsDocsTraversal ensures the add command surfaces a docs-escapes-root
// rejection from the registry rather than registering it.
func TestAddRejectsDocsTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"add", root, "../escape"}, &out); err == nil {
		t.Fatal("add with a traversing docs subpath should error")
	}
}

// TestAddAgentCmdOverridesPreset verifies --agent-cmd wins over --agent: the
// stored per-project agent command is the explicit --agent-cmd string, not the
// preset it would otherwise resolve.
// TestAddAgentCmdRegistersCustomMember: --agent-cmd registers a member by raw
// command. In the council model --agent and --agent-cmd both APPEND members (they
// don't "override"), so a single custom-command agent is expressed with
// --agent-cmd on its own — one member, single-agent mode (not council).
func TestAddAgentCmdRegistersCustomMember(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()

	var out bytes.Buffer
	if err := run([]string{"add", root, ".", "--agent-cmd", "custom {prompt}"}, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out.String(), "custom {prompt}") {
		t.Fatalf("add output should show the custom command:\n%s", out.String())
	}
	// Stored as a single custom-command member; single-agent mode, no council.
	list, err := registry.List(filepath.Join(home, ".config", "outbox", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AgentCmd() != "custom {prompt}" || list[0].IsCouncil() {
		t.Fatalf("stored = %+v, want a single custom-command member (no council)", list)
	}
}

// TestAddZeroDocsFailsWithHelp verifies `outbox add <root>` (no docs) fails with a
// non-nil error AND prints the add help/examples — a docs arg is mandatory.
func TestAddZeroDocsFailsWithHelp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"add", root}, &out); err == nil {
		t.Fatal("add with no docs must error")
	}
	if !strings.Contains(out.String(), "Examples:") || !strings.Contains(out.String(), "outbox add") {
		t.Fatalf("add-with-no-docs should print the add help/examples:\n%s", out.String())
	}
	// Nothing registered.
	if list, _ := registry.List(filepath.Join(home, ".config", "outbox", "projects.json")); len(list) != 0 {
		t.Fatalf("a rejected add must register nothing, got %v", list)
	}
}

// TestAddMultipleDocsCLI verifies `outbox add <root> d1 d2` registers both
// subpaths as one project and `outbox list` shows both locations.
func TestAddMultipleDocsCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	for _, d := range []string{"specs", "api-specs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var addOut bytes.Buffer
	if err := run([]string{"add", root, "specs", "api-specs"}, &addOut); err != nil {
		t.Fatalf("add: %v", err)
	}
	var listOut bytes.Buffer
	if err := run([]string{"list"}, &listOut); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut.String(), "specs") || !strings.Contains(listOut.String(), "api-specs") {
		t.Fatalf("list should show both docs subpaths:\n%s", listOut.String())
	}
}

// TestRemoveWholeProjectByName verifies `outbox remove <name>` removes the whole
// matching project (back-compat shortcut) and `list` then reports none.
func TestRemoveWholeProjectByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	root := t.TempDir()
	for _, d := range []string{"specs", "api-specs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.Add(filepath.Join(home, ".config", "outbox", "projects.json"), root, []string{"specs", "api-specs"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(root)

	var out bytes.Buffer
	if err := removeProject([]string{name}, &out, strings.NewReader("")); err != nil {
		t.Fatalf("remove <name>: %v", err)
	}
	if !strings.Contains(out.String(), "removed project") {
		t.Fatalf("remove output = %q", out.String())
	}
	// The whole project (both docs) is gone.
	var listOut bytes.Buffer
	if err := run([]string{"list"}, &listOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listOut.String(), "no projects registered") {
		t.Fatalf("list after remove should be empty:\n%s", listOut.String())
	}
}

// TestRemoveUnknownNameErrors verifies `outbox remove <unknown>` is an error (not a
// silent no-op).
func TestRemoveUnknownNameErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var out bytes.Buffer
	if err := removeProject([]string{"ghost"}, &out, strings.NewReader("")); err == nil {
		t.Fatal("remove of an unknown project should error")
	}
}

// TestRemoveNonTTYNoArgGuard verifies `outbox remove` with no argument and a
// non-terminal stdin returns an error (never hangs) with the guidance message.
func TestRemoveNonTTYNoArgGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var out bytes.Buffer
	err := removeProject(nil, &out, strings.NewReader(""))
	if err == nil {
		t.Fatal("remove with no arg on a non-TTY stdin should error, not hang")
	}
	if !strings.Contains(err.Error(), "non-interactive") {
		t.Fatalf("guard error = %q, want the non-interactive hint", err.Error())
	}
}

// TestBuildRemoveRowsFlattensDocs verifies the multiselect rows are one per
// (project, docs) pair, in registry order — the docs-granular row model.
func TestBuildRemoveRowsFlattensDocs(t *testing.T) {
	projects := []registry.Project{
		{Name: "outbox-md", Root: "/o", Docs: []string{"docs/specs", "api-specs"}},
		{Name: "other", Root: "/x", Docs: []string{"."}},
	}
	rows := buildRemoveRows(projects)
	want := []string{"outbox-md · docs/specs", "outbox-md · api-specs", "other · ."}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].label() != w {
			t.Fatalf("row %d = %q, want %q", i, rows[i].label(), w)
		}
	}
}

// TestBareInvocationPrintsHelp verifies `outbox` with no args prints help (and
// does NOT start a server), and returns no error.
func TestBareInvocationPrintsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, &out); err != nil {
		t.Fatalf("bare invocation should not error: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Usage:") || !strings.Contains(s, "Commands") {
		t.Fatalf("bare invocation should print help:\n%s", s)
	}
	if strings.Contains(s, "serving") {
		t.Fatalf("bare invocation must not start a server:\n%s", s)
	}
}

// TestEveryCommandHasExamples asserts each documented command's help carries an
// EXAMPLES section (the help-first ergonomics requirement).
func TestEveryCommandHasExamples(t *testing.T) {
	for _, name := range commandNames() {
		var out bytes.Buffer
		if err := run([]string{"help", name}, &out); err != nil {
			t.Fatalf("help %s: %v", name, err)
		}
		if !strings.Contains(out.String(), "Examples:") {
			t.Fatalf("command %q help is missing an Examples section:\n%s", name, out.String())
		}
	}
}

// TestPathsCmd verifies `outbox paths` prints the registry, review database and
// config locations, and is mode-aware.
func TestPathsCmd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// Single-dir mode (no projects registered).
	var single bytes.Buffer
	if err := run([]string{"paths"}, &single); err != nil {
		t.Fatalf("paths: %v", err)
	}
	if !strings.Contains(single.String(), "projects.json") ||
		!strings.Contains(single.String(), "outbox.db") ||
		!strings.Contains(single.String(), "single-dir") {
		t.Fatalf("single-dir paths output incomplete:\n%s", single.String())
	}

	// Register a project → multi-project mode.
	root := t.TempDir()
	if _, err := registry.Add(filepath.Join(home, ".config", "outbox", "projects.json"), root, []string{"."}, nil, ""); err != nil {
		t.Fatal(err)
	}
	var multi bytes.Buffer
	if err := run([]string{"paths"}, &multi); err != nil {
		t.Fatalf("paths (multi): %v", err)
	}
	if !strings.Contains(multi.String(), "multi-project") ||
		!strings.Contains(multi.String(), filepath.Join(root, "outbox.yaml")) {
		t.Fatalf("multi-project paths output incomplete:\n%s", multi.String())
	}
}

// TestSettingsSetAndNonTTY verifies `outbox settings <key> <value>` writes the
// field, and the interactive form on a non-TTY stdin prints settings without
// hanging.
func TestSettingsSetAndNonTTY(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "outbox.yaml")
	// Seed a config with an unmanaged key to prove round-trip preservation.
	if err := os.WriteFile(cfgPath, []byte("sources:\n  - docs/specs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// settings operates on ./outbox.yaml — run from dir.
	restore := chdir(t, dir)
	defer restore()

	var setOut bytes.Buffer
	if err := run([]string{"settings", "auto_reply", "true"}, &setOut); err != nil {
		t.Fatalf("settings set: %v", err)
	}
	if !strings.Contains(setOut.String(), "auto_reply = true") {
		t.Fatalf("settings set output = %q", setOut.String())
	}
	// The write must set auto_reply AND preserve the unmanaged sources key.
	b, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(b), "auto_reply") || !strings.Contains(string(b), "docs/specs") {
		t.Fatalf("outbox.yaml did not preserve sources / set auto_reply:\n%s", b)
	}

	// Interactive form with a non-TTY stdin (a bytes.Reader) must not hang; it
	// prints the current settings and returns nil.
	var iOut bytes.Buffer
	if err := settingsCmd(nil, &iOut, strings.NewReader("")); err != nil {
		t.Fatalf("settings non-TTY: %v", err)
	}
	if !strings.Contains(iOut.String(), "auto_reply") {
		t.Fatalf("non-TTY settings should print current settings:\n%s", iOut.String())
	}

	// An unknown key is rejected.
	if err := run([]string{"settings", "bogus", "1"}, &bytes.Buffer{}); err == nil {
		t.Fatal("settings with an unknown key should error")
	}
}

// TestSettingsRequiresInit verifies `outbox settings` errors (pointing at init)
// when there is no ./outbox.yaml.
func TestSettingsRequiresInit(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	if err := run([]string{"settings", "auto_reply", "true"}, &bytes.Buffer{}); err == nil {
		t.Fatal("settings without an outbox.yaml should error")
	}
}

// TestSettingsOnCommentsOnlyInitFile is the documented happy path: `outbox init`
// scaffolds a comments-only outbox.yaml (no real keys), and `outbox settings
// <key> <value>` on it must succeed (not error on "not a mapping"), add the key,
// and preserve the starter comments.
func TestSettingsOnCommentsOnlyInitFile(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()
	// starterConfig is exactly what `outbox init` writes — all comments, no keys.
	if err := os.WriteFile("outbox.yaml", []byte(starterConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"settings", "auto_reply", "true"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("settings on a comments-only init file should succeed, got: %v", err)
	}
	b, _ := os.ReadFile("outbox.yaml")
	s := string(b)
	if !strings.Contains(s, "auto_reply: true") {
		t.Fatalf("auto_reply not written:\n%s", s)
	}
	// Starter guidance comments must survive the write.
	if !strings.Contains(s, "# outbox.yaml") {
		t.Fatalf("starter comments were clobbered:\n%s", s)
	}
	// A second set now sees a real mapping and appends the other key cleanly.
	if err := run([]string{"settings", "auto_update", "false"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("second settings set should succeed, got: %v", err)
	}
	b, _ = os.ReadFile("outbox.yaml")
	if s := string(b); !strings.Contains(s, "auto_reply: true") || !strings.Contains(s, "auto_update: false") {
		t.Fatalf("both keys should be present after two sets:\n%s", s)
	}
}

// chdir changes to dir for the duration of a test, returning a restore func.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(prev) }
}

// --- outbox retry ---

// TestRetryCmdMultiProject seeds a claimed comment in a registered project's
// shared DB and confirms `outbox retry` (no arg) re-queues it and reports the
// count per project.
func TestRetryCmdMultiProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)

	projDir := t.TempDir()
	p, err := registry.Add(registryPath(), projDir, []string{"."}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configHomeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(configHomeDir(), "outbox.db")
	st, err := store.Open("file:" + dbFile)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, _ := st.CreateDocumentInProject(p.Name, "a.md", "hi", "human")
	c, _ := st.CreateComment(domain.Comment{DocID: doc.ID, Status: domain.CommentOpen, AuthorIdentity: "h", Owner: "h"})
	if err := st.UpdateCommentStatus(c.ID, domain.CommentClaimed, "tok"); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var out bytes.Buffer
	if err := retryCmd(nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "re-queued 1 in "+p.Name) {
		t.Fatalf("retry output = %q, want \"re-queued 1 in %s\"", out.String(), p.Name)
	}

	st2, _ := store.Open("file:" + dbFile)
	defer st2.Close()
	got, _ := st2.GetComment(c.ID)
	if got.Status != domain.CommentOpen || got.ClaimToken != "" {
		t.Fatalf("comment not re-queued: status=%q token=%q", got.Status, got.ClaimToken)
	}
}

// TestRetryCmdNamedProject targets a single project by name.
func TestRetryCmdNamedProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	projDir := t.TempDir()
	p, err := registry.Add(registryPath(), projDir, []string{"."}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configHomeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open("file:" + filepath.Join(configHomeDir(), "outbox.db"))
	doc, _, _ := st.CreateDocumentInProject(p.Name, "a.md", "hi", "human")
	c, _ := st.CreateComment(domain.Comment{DocID: doc.ID, Status: domain.CommentOpen, AuthorIdentity: "h", Owner: "h"})
	_ = st.UpdateCommentStatus(c.ID, domain.CommentClaimed, "tok")
	_ = st.Close()

	var out bytes.Buffer
	if err := retryCmd([]string{p.Name}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "re-queued 1 in "+p.Name) {
		t.Fatalf("named retry output = %q", out.String())
	}
}

// TestRetryCmdUnknownProjectErrors: an unregistered name fails with the retry
// help, even before any DB is opened.
func TestRetryCmdUnknownProjectErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	projDir := t.TempDir()
	if _, err := registry.Add(registryPath(), projDir, []string{"."}, nil, ""); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := retryCmd([]string{"does-not-exist"}, &out); err == nil {
		t.Fatal("unknown project should error")
	}
	if !strings.Contains(out.String(), "outbox retry") {
		t.Fatalf("unknown project should print retry help, got %q", out.String())
	}
}

// TestRetryCmdSingleFolder: with no registry, retry targets the single-folder DB
// under OUTBOX_DIR and prints a bare count.
func TestRetryCmdSingleFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home) // empty registry ⇒ single-folder mode
	dir := t.TempDir()
	t.Setenv("OUTBOX_DIR", dir)

	if err := os.MkdirAll(filepath.Join(dir, ".outbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open("file:" + filepath.Join(dir, ".outbox", "outbox.db"))
	doc, _, _ := st.CreateDocumentInProject("", "a.md", "hi", "human")
	c, _ := st.CreateComment(domain.Comment{DocID: doc.ID, Status: domain.CommentOpen, AuthorIdentity: "h", Owner: "h"})
	_ = st.UpdateCommentStatus(c.ID, domain.CommentClaimed, "tok")
	_ = st.Close()

	var out bytes.Buffer
	if err := retryCmd(nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "re-queued 1" {
		t.Fatalf("single-folder retry output = %q, want \"re-queued 1\"", got)
	}
}

// TestRetryCmdNoDatabase: a missing review database is reported, not created.
func TestRetryCmdNoDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("OUTBOX_DIR", t.TempDir())
	var out bytes.Buffer
	if err := retryCmd(nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no review database") {
		t.Fatalf("missing DB output = %q, want a 'no review database' notice", out.String())
	}
}

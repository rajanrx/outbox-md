package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// regFile returns a registry path inside a fresh temp dir (the file itself does
// not exist yet — Load must treat that as an empty registry).
func regFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "projects.json")
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	got, err := Load(regFile(t))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing registry should be empty, got %v", got)
	}
}

// TestAddRootDocsAgent registers a project with a docs subpath and a per-project
// agent, and verifies every field round-trips.
func TestAddRootDocsAgent(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	spec := filepath.Join(root, "docs", "specs")
	if err := os.MkdirAll(spec, 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := Add(file, root, "docs/specs", "codex exec {prompt}")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if p.Name != filepath.Base(root) {
		t.Fatalf("name = %q, want basename %q", p.Name, filepath.Base(root))
	}
	if !filepath.IsAbs(p.Root) {
		t.Fatalf("root %q is not absolute", p.Root)
	}
	if p.Docs != "docs/specs" {
		t.Fatalf("docs = %q, want docs/specs", p.Docs)
	}
	if p.Agent != "codex exec {prompt}" {
		t.Fatalf("agent = %q, want the codex command", p.Agent)
	}
	if got, want := p.SpecDir(), spec; got != want {
		t.Fatalf("SpecDir = %q, want %q", got, want)
	}

	list, err := List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Root != p.Root || list[0].Docs != "docs/specs" {
		t.Fatalf("list = %v, want single entry %v", list, p)
	}
}

// TestAddDefaultDocsIsRoot verifies an omitted docs defaults to "." (serve the
// whole root) and an omitted agent stays empty (inherit the global default).
func TestAddDefaultDocsIsRoot(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	p, err := Add(file, root, "", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if p.Docs != "." {
		t.Fatalf("docs = %q, want .", p.Docs)
	}
	if p.Agent != "" {
		t.Fatalf("agent = %q, want empty", p.Agent)
	}
	if p.SpecDir() != p.Root {
		t.Fatalf("SpecDir = %q, want root %q", p.SpecDir(), p.Root)
	}
}

func TestAddIsIdempotentByRootAndDocs(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	if _, err := Add(file, root, ".", ""); err != nil {
		t.Fatal(err)
	}
	// Adding the same (root, docs) again must not duplicate it.
	if _, err := Add(file, root, ".", ""); err != nil {
		t.Fatal(err)
	}
	list, _ := List(file)
	if len(list) != 1 {
		t.Fatalf("dedupe failed: %d entries, want 1", len(list))
	}
}

func TestAddMissingRootErrors(t *testing.T) {
	file := regFile(t)
	if _, err := Add(file, filepath.Join(t.TempDir(), "does-not-exist"), ".", ""); err == nil {
		t.Fatal("expected error adding a missing directory")
	}
}

func TestAddFileNotDirErrors(t *testing.T) {
	file := regFile(t)
	f := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(file, f, ".", ""); err == nil {
		t.Fatal("expected error adding a file (not a directory)")
	}
}

// TestAddDocsTraversalRejected verifies a docs subpath that escapes the root
// (via ../ or an absolute path) is rejected — the server must never serve or
// write outside the project root.
func TestAddDocsTraversalRejected(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	for _, docs := range []string{"../evil", "../../etc", "/etc"} {
		if _, err := Add(file, root, docs, ""); err == nil {
			t.Fatalf("docs %q should be rejected as traversal", docs)
		}
	}
	// A docs pointing at a non-existent (but non-escaping) dir is also rejected.
	if _, err := Add(file, root, "nope", ""); err == nil {
		t.Fatal("docs pointing at a missing dir should be rejected")
	}
}

// TestAddDisambiguatesNameCollision verifies two different roots that share a
// basename get distinct names — routing writes by name demands uniqueness.
func TestAddDisambiguatesNameCollision(t *testing.T) {
	file := regFile(t)
	base := t.TempDir()
	a := filepath.Join(base, "x", "docs")
	b := filepath.Join(base, "y", "docs")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pa, err := Add(file, a, ".", "")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Add(file, b, ".", "")
	if err != nil {
		t.Fatal(err)
	}
	if pa.Name == pb.Name {
		t.Fatalf("collision not disambiguated: both named %q", pa.Name)
	}
	if pa.Name != "docs" || pb.Name != "docs-2" {
		t.Fatalf("names = %q, %q; want docs, docs-2", pa.Name, pb.Name)
	}
}

func TestRemoveByNameAndRoot(t *testing.T) {
	file := regFile(t)
	d1, d2 := t.TempDir(), t.TempDir()
	p1, _ := Add(file, d1, ".", "")
	p2, _ := Add(file, d2, ".", "")

	// Remove by name.
	removed, err := Remove(file, p1.Name)
	if err != nil || !removed {
		t.Fatalf("remove by name: removed=%v err=%v", removed, err)
	}
	// Remove by root.
	removed, err = Remove(file, p2.Root)
	if err != nil || !removed {
		t.Fatalf("remove by root: removed=%v err=%v", removed, err)
	}
	list, _ := List(file)
	if len(list) != 0 {
		t.Fatalf("registry not empty after removals: %v", list)
	}
}

func TestRemoveUnknownIsNoError(t *testing.T) {
	file := regFile(t)
	removed, err := Remove(file, "ghost")
	if err != nil {
		t.Fatalf("remove unknown: %v", err)
	}
	if removed {
		t.Fatal("removed should be false for an unknown ref")
	}
}

// TestMigrateLegacyPathEntry verifies an older {name,path} registry loads as the
// new shape: root ← path, docs ← ".", agent ← "", name preserved.
func TestMigrateLegacyPathEntry(t *testing.T) {
	file := regFile(t)
	legacy := `[{"name":"docs","path":"/work/app/docs"}]`
	if err := os.WriteFile(file, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := Load(file)
	if err != nil {
		t.Fatalf("Load legacy: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("legacy load = %d entries, want 1", len(list))
	}
	p := list[0]
	if p.Name != "docs" {
		t.Fatalf("name = %q, want docs", p.Name)
	}
	if p.Root != "/work/app/docs" {
		t.Fatalf("root = %q, want the legacy path", p.Root)
	}
	if p.Docs != "." {
		t.Fatalf("docs = %q, want .", p.Docs)
	}
	if p.Agent != "" {
		t.Fatalf("agent = %q, want empty", p.Agent)
	}
	if p.SpecDir() != "/work/app/docs" {
		t.Fatalf("SpecDir = %q, want the legacy path", p.SpecDir())
	}
}

// TestMigrateLegacyEntryWithoutName verifies a legacy entry lacking a name gets
// basename(path) so it stays labelled and routable.
func TestMigrateLegacyEntryWithoutName(t *testing.T) {
	file := regFile(t)
	if err := os.WriteFile(file, []byte(`[{"path":"/work/app"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "app" || list[0].Root != "/work/app" {
		t.Fatalf("migrated = %v, want name app / root /work/app", list)
	}
}

// TestSaveWritesNewShape verifies Save persists the {name,root,docs,agent} shape
// (and never the legacy path key), so a migrated registry is rewritten forward.
func TestSaveWritesNewShape(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	if _, err := Add(file, root, ".", "claude -p {prompt}"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"name"`, `"root"`, `"docs"`, `"agent"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("saved registry missing %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"path"`) {
		t.Fatalf("saved registry still has the legacy path key:\n%s", s)
	}
}

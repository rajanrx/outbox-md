package registry

import (
	"errors"
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

	p, err := Add(file, root, []string{"docs/specs"}, []Member{{Agent: "codex exec {prompt}"}}, Member{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if p.Name != filepath.Base(root) {
		t.Fatalf("name = %q, want basename %q", p.Name, filepath.Base(root))
	}
	if !filepath.IsAbs(p.Root) {
		t.Fatalf("root %q is not absolute", p.Root)
	}
	if len(p.Docs) != 1 || p.Docs[0] != "docs/specs" {
		t.Fatalf("docs = %v, want [docs/specs]", p.Docs)
	}
	if len(p.Members) != 1 || p.Members[0].Agent != "codex exec {prompt}" {
		t.Fatalf("members = %v, want the codex command", p.Members)
	}
	if p.AgentCmd() != "codex exec {prompt}" {
		t.Fatalf("AgentCmd = %q, want the codex command", p.AgentCmd())
	}
	if p.IsCouncil() {
		t.Fatalf("IsCouncil = true, want false for a single member")
	}
	if dirs := p.SpecDirs(); len(dirs) != 1 || dirs[0] != spec {
		t.Fatalf("SpecDirs = %v, want [%q]", dirs, spec)
	}

	list, err := List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Root != p.Root || len(list[0].Docs) != 1 || list[0].Docs[0] != "docs/specs" {
		t.Fatalf("list = %v, want single entry %v", list, p)
	}
}

// TestAddExplicitDotServesRoot verifies an explicit "." docs entry (opt into a
// docs-only whole-repo project) serves the whole root, and an omitted agent stays
// empty (inherit the global default).
func TestAddExplicitDotServesRoot(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	p, err := Add(file, root, []string{"."}, nil, Member{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(p.Docs) != 1 || p.Docs[0] != "." {
		t.Fatalf("docs = %v, want [.]", p.Docs)
	}
	if len(p.Members) != 0 || p.AgentCmd() != "" {
		t.Fatalf("members = %v / AgentCmd = %q, want empty (inherit global default)", p.Members, p.AgentCmd())
	}
	if dirs := p.SpecDirs(); len(dirs) != 1 || dirs[0] != p.Root {
		t.Fatalf("SpecDirs = %v, want [root %q]", dirs, p.Root)
	}
}

// TestAddZeroDocsRejected verifies that Add requires at least one docs entry —
// an empty (or nil) docs list is an error, not a default to ".".
func TestAddZeroDocsRejected(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	if _, err := Add(file, root, nil, nil, Member{}); err == nil {
		t.Fatal("expected error adding with a nil docs list")
	}
	if _, err := Add(file, root, []string{}, nil, Member{}); err == nil {
		t.Fatal("expected error adding with an empty docs list")
	}
	if list, _ := List(file); len(list) != 0 {
		t.Fatalf("a rejected add must not register anything, got %v", list)
	}
}

// TestAddMultipleDocsStoresAll verifies `add <root> d1 d2` stores both subpaths
// (deduped), and the project serves the union.
func TestAddMultipleDocsStoresAll(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	for _, d := range []string{"specs", "api-specs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A duplicate entry is deduped, not stored twice.
	p, err := Add(file, root, []string{"specs", "api-specs", "specs"}, nil, Member{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(p.Docs) != 2 || p.Docs[0] != "specs" || p.Docs[1] != "api-specs" {
		t.Fatalf("docs = %v, want [specs api-specs] (deduped)", p.Docs)
	}
	dirs := p.SpecDirs()
	if len(dirs) != 2 ||
		dirs[0] != filepath.Join(root, "specs") ||
		dirs[1] != filepath.Join(root, "api-specs") {
		t.Fatalf("SpecDirs = %v, want the two subpaths joined to root", dirs)
	}
}

// TestAddBadDocsAmongGoodRejected verifies a single non-existent docs entry fails
// the whole add (all-or-nothing), leaving nothing registered.
func TestAddBadDocsAmongGoodRejected(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(file, root, []string{"specs", "nope"}, nil, Member{}); err == nil {
		t.Fatal("expected error: a missing docs entry among good ones must fail the add")
	}
	if list, _ := List(file); len(list) != 0 {
		t.Fatalf("a partially-invalid add must register nothing, got %v", list)
	}
}

func TestAddIsIdempotentByRootAndDocs(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	if _, err := Add(file, root, []string{"."}, nil, Member{}); err != nil {
		t.Fatal(err)
	}
	// Adding the same (root, docs) again must not duplicate it.
	if _, err := Add(file, root, []string{"."}, nil, Member{}); err != nil {
		t.Fatal(err)
	}
	list, _ := List(file)
	if len(list) != 1 {
		t.Fatalf("dedupe failed: %d entries, want 1", len(list))
	}
}

// TestAddIdempotentRegardlessOfDocsOrder — re-adding a root with the same docs in
// a different order must dedupe (equalDocs is set-based, not order-sensitive), so
// `add root specs api-specs` then `add root api-specs specs` is one entry, not two.
func TestAddIdempotentRegardlessOfDocsOrder(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	for _, d := range []string{"specs", "api-specs"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(file, root, []string{"specs", "api-specs"}, nil, Member{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(file, root, []string{"api-specs", "specs"}, nil, Member{}); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(file); len(list) != 1 {
		t.Fatalf("reordered docs should dedupe: %d entries, want 1", len(list))
	}
}

func TestAddMissingRootErrors(t *testing.T) {
	file := regFile(t)
	if _, err := Add(file, filepath.Join(t.TempDir(), "does-not-exist"), []string{"."}, nil, Member{}); err == nil {
		t.Fatal("expected error adding a missing directory")
	}
}

func TestAddFileNotDirErrors(t *testing.T) {
	file := regFile(t)
	f := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(file, f, []string{"."}, nil, Member{}); err == nil {
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
		if _, err := Add(file, root, []string{docs}, nil, Member{}); err == nil {
			t.Fatalf("docs %q should be rejected as traversal", docs)
		}
	}
	// A docs pointing at a non-existent (but non-escaping) dir is also rejected.
	if _, err := Add(file, root, []string{"nope"}, nil, Member{}); err == nil {
		t.Fatal("docs pointing at a missing dir should be rejected")
	}
}

// TestAddRejectsSymlinkedDocsEscapingRoot verifies a docs subpath that passes the
// lexical ../ check but resolves outside root via a symlink is rejected — the
// containment check must run on the symlink-resolved paths, not just lexically.
func TestAddRejectsSymlinkedDocsEscapingRoot(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	outside := t.TempDir() // a real dir OUTSIDE root
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unsupported here: " + err.Error())
	}
	if _, err := Add(file, root, []string{"link"}, nil, Member{}); err == nil {
		t.Fatal("docs symlink escaping root should be rejected")
	}
	// A real subdir under root is still accepted (fix doesn't over-reject).
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(file, root, []string{"docs"}, nil, Member{}); err != nil {
		t.Fatalf("legit docs subdir wrongly rejected: %v", err)
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
	pa, err := Add(file, a, []string{"."}, nil, Member{})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Add(file, b, []string{"."}, nil, Member{})
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
	p1, _ := Add(file, d1, []string{"."}, nil, Member{})
	p2, _ := Add(file, d2, []string{"."}, nil, Member{})

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

// TestApplyRemovalsTrimsProject verifies removing ONE of a project's docs entries
// leaves the project registered with the remaining entries — not dropped.
func TestApplyRemovalsTrimsProject(t *testing.T) {
	projects := []Project{{Name: "app", Root: "/work/app", Docs: []string{"specs", "api-specs"}}}
	kept, applied := ApplyRemovals(projects, []DocRemoval{{Project: "app", Docs: "specs"}})
	if len(kept) != 1 {
		t.Fatalf("kept = %d projects, want 1", len(kept))
	}
	if len(kept[0].Docs) != 1 || kept[0].Docs[0] != "api-specs" {
		t.Fatalf("docs = %v, want [api-specs]", kept[0].Docs)
	}
	if len(applied) != 1 || applied[0].Project != "app" || applied[0].Docs != "specs" {
		t.Fatalf("applied = %v, want [{app specs}]", applied)
	}
}

// TestApplyRemovalsDropsProjectOnLastDocs verifies removing a project's LAST docs
// entry drops the whole project.
func TestApplyRemovalsDropsProjectOnLastDocs(t *testing.T) {
	projects := []Project{{Name: "app", Root: "/work/app", Docs: []string{"specs"}}}
	kept, applied := ApplyRemovals(projects, []DocRemoval{{Project: "app", Docs: "specs"}})
	if len(kept) != 0 {
		t.Fatalf("kept = %v, want the project dropped", kept)
	}
	if len(applied) != 1 {
		t.Fatalf("applied = %v, want one removal", applied)
	}
}

// TestApplyRemovalsBatchSpanningProjects is the discriminating case: a batch that
// removes ALL docs of one project (dropping it) AND some docs of another (trimming
// it) in a single pass. A naive per-removal "delete then check empty" mis-handles
// this; verify both outcomes.
func TestApplyRemovalsBatchSpanningProjects(t *testing.T) {
	projects := []Project{
		{Name: "gone", Root: "/g", Docs: []string{"a", "b"}},
		{Name: "trim", Root: "/t", Docs: []string{"x", "y", "z"}},
		{Name: "keep", Root: "/k", Docs: []string{"."}},
	}
	removals := []DocRemoval{
		{Project: "gone", Docs: "a"},
		{Project: "gone", Docs: "b"},
		{Project: "trim", Docs: "y"},
	}
	kept, applied := ApplyRemovals(projects, removals)
	if len(kept) != 2 {
		t.Fatalf("kept = %v, want 2 (gone dropped)", kept)
	}
	if kept[0].Name != "trim" || len(kept[0].Docs) != 2 || kept[0].Docs[0] != "x" || kept[0].Docs[1] != "z" {
		t.Fatalf("trim project = %+v, want docs [x z]", kept[0])
	}
	if kept[1].Name != "keep" || len(kept[1].Docs) != 1 || kept[1].Docs[0] != "." {
		t.Fatalf("keep project = %+v, want docs [.] untouched", kept[1])
	}
	if len(applied) != 3 {
		t.Fatalf("applied = %v, want all 3 removals", applied)
	}
}

// TestApplyRemovalsPureNoInputMutation verifies ApplyRemovals does not mutate the
// caller's input slice/entries — it operates on copies.
func TestApplyRemovalsPureNoInputMutation(t *testing.T) {
	orig := []Project{{Name: "app", Root: "/work/app", Docs: []string{"specs", "api-specs"}}}
	_, _ = ApplyRemovals(orig, []DocRemoval{{Project: "app", Docs: "specs"}})
	if len(orig) != 1 || len(orig[0].Docs) != 2 {
		t.Fatalf("input mutated: %+v", orig)
	}
}

// TestApplyRemovalsIgnoresUnknown verifies removals naming an unknown project or
// docs entry are simply ignored (no drop, no panic, nothing applied).
func TestApplyRemovalsIgnoresUnknown(t *testing.T) {
	projects := []Project{{Name: "app", Root: "/work/app", Docs: []string{"specs"}}}
	kept, applied := ApplyRemovals(projects, []DocRemoval{
		{Project: "ghost", Docs: "specs"},
		{Project: "app", Docs: "nope"},
	})
	if len(kept) != 1 || len(kept[0].Docs) != 1 || kept[0].Docs[0] != "specs" {
		t.Fatalf("kept = %+v, want the project untouched", kept)
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %v, want none", applied)
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
	if len(p.Docs) != 1 || p.Docs[0] != "." {
		t.Fatalf("docs = %v, want [.]", p.Docs)
	}
	if len(p.Members) != 0 {
		t.Fatalf("members = %v, want empty", p.Members)
	}
	if dirs := p.SpecDirs(); len(dirs) != 1 || dirs[0] != "/work/app/docs" {
		t.Fatalf("SpecDirs = %v, want [the legacy path]", dirs)
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

// TestLoadMixedAndMalformed covers the data-loss surface: a file mixing legacy
// {name,path} and current {name,root,docs} entries loads BOTH (no drops), and a
// malformed file fails safe (error, never a silent empty registry).
func TestLoadMixedAndMalformed(t *testing.T) {
	file := regFile(t)
	mixed := `[{"name":"legacy","path":"/old/repo"},` +
		`{"name":"modern","root":"/new/repo","docs":"docs/specs","agent":"claude -p {prompt}"}]`
	if err := os.WriteFile(file, []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := Load(file)
	if err != nil {
		t.Fatalf("Load errored on a mixed file: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("mixed file lost entries: got %d, want 2 (%v)", len(list), list)
	}
	if list[0].Root != "/old/repo" || len(list[0].Docs) != 1 || list[0].Docs[0] != "." {
		t.Errorf("legacy entry not migrated: %+v", list[0])
	}
	if list[1].Root != "/new/repo" || len(list[1].Docs) != 1 || list[1].Docs[0] != "docs/specs" {
		t.Errorf("modern entry corrupted: %+v", list[1])
	}
	if err := os.WriteFile(file, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("malformed registry should error, not silently return an empty list")
	}
}

// TestSaveWritesNewShape verifies Save persists the {name,root,docs,members} shape
// (and never the legacy path/agent keys), so a migrated registry is rewritten
// forward.
func TestSaveWritesNewShape(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	if _, err := Add(file, root, []string{"."}, []Member{{Agent: "claude -p {prompt}"}}, Member{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"name"`, `"root"`, `"docs"`, `"members"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("saved registry missing %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"path"`) {
		t.Fatalf("saved registry still has the legacy path key:\n%s", s)
	}
	// docs must serialise as a JSON array, not a bare string.
	if !strings.Contains(s, `"docs": [`) {
		t.Fatalf("saved registry docs is not a list:\n%s", s)
	}
}

// TestLoadDocsShapes covers the three docs on-disk shapes the loader must accept:
// a single string (#59), a list (current), and an empty/missing list (→ ["."]).
func TestLoadDocsShapes(t *testing.T) {
	file := regFile(t)
	raw := `[` +
		`{"name":"single","root":"/a","docs":"specs"},` +
		`{"name":"list","root":"/b","docs":["specs","api-specs"]},` +
		`{"name":"empty","root":"/c","docs":[]},` +
		`{"name":"nulldocs","root":"/d"}]`
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := Load(file)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("loaded %d entries, want 4", len(list))
	}
	if got := list[0].Docs; len(got) != 1 || got[0] != "specs" {
		t.Errorf("single-string docs = %v, want [specs]", got)
	}
	if got := list[1].Docs; len(got) != 2 || got[0] != "specs" || got[1] != "api-specs" {
		t.Errorf("list docs = %v, want [specs api-specs]", got)
	}
	if got := list[2].Docs; len(got) != 1 || got[0] != "." {
		t.Errorf("empty-list docs = %v, want [.]", got)
	}
	if got := list[3].Docs; len(got) != 1 || got[0] != "." {
		t.Errorf("missing docs = %v, want [.]", got)
	}
}

// TestAddCouncilMembersAndChair registers a council (two members + a chair) and
// verifies every field round-trips through Save/Load, IsCouncil is true, and the
// persisted file carries the new members/chair shape.
func TestAddCouncilMembersAndChair(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()

	members := []Member{
		{Agent: "claude -p {prompt} --allowedTools mcp__outbox-md__*"},
		{Agent: "codex exec {prompt}"},
	}
	chair := Member{Agent: "claude -p {prompt} --allowedTools mcp__outbox-md__*"}
	p, err := Add(file, root, []string{"."}, members, chair)
	if err != nil {
		t.Fatalf("Add council: %v", err)
	}
	if !p.IsCouncil() {
		t.Fatalf("IsCouncil = false, want true for two members")
	}
	if got := p.MemberCmds(); len(got) != 2 || got[0] != members[0].Agent || got[1] != members[1].Agent {
		t.Fatalf("MemberCmds = %v, want the two member commands", got)
	}
	if p.ChairCmd() != chair.Agent {
		t.Fatalf("ChairCmd = %q, want %q", p.ChairCmd(), chair.Agent)
	}
	if p.AgentCmd() != members[0].Agent {
		t.Fatalf("AgentCmd = %q, want the first member %q", p.AgentCmd(), members[0].Agent)
	}

	// Round-trip through disk: the council survives Load, and Save wrote the new
	// members/chair keys.
	list, err := List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].IsCouncil() || len(list[0].Members) != 2 || list[0].ChairCmd() != chair.Agent {
		t.Fatalf("reloaded council = %+v, want 2 members + chair", list)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"members"`) || !strings.Contains(s, `"chair"`) {
		t.Fatalf("saved council missing members/chair keys:\n%s", s)
	}
}

// TestAddCouncilRequiresChair verifies the council rule: two or more members with
// no chair is rejected at registration (with the ErrChairRequired sentinel).
func TestAddCouncilRequiresChair(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	_, err := Add(file, root, []string{"."}, []Member{{Agent: "claude -p {prompt}"}, {Agent: "codex exec {prompt}"}}, Member{})
	if err == nil {
		t.Fatal("expected error adding a council (>=2 members) without a chair")
	}
	if !errors.Is(err, ErrChairRequired) {
		t.Fatalf("err = %v, want ErrChairRequired", err)
	}
	// A single member with no chair is fine (single-agent mode).
	if _, err := Add(file, root, []string{"."}, []Member{{Agent: "claude -p {prompt}"}}, Member{}); err != nil {
		t.Fatalf("single member without a chair should succeed: %v", err)
	}
}

// TestMigrateLegacyAgentToAgents verifies a legacy single-string {agent:"x"} entry
// migrates to Agents:["x"] on read, staying single-agent (no chair).
func TestMigrateLegacyAgentToAgents(t *testing.T) {
	file := regFile(t)
	legacy := `[{"name":"app","root":"/work/app","docs":["."],"agent":"codex exec {prompt}"}]`
	if err := os.WriteFile(file, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := Load(file)
	if err != nil {
		t.Fatalf("Load legacy agent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("legacy load = %d entries, want 1", len(list))
	}
	p := list[0]
	if len(p.Members) != 1 || p.Members[0].Agent != "codex exec {prompt}" {
		t.Fatalf("members = %v, want [{codex exec {prompt}}]", p.Members)
	}
	if p.AgentCmd() != "codex exec {prompt}" {
		t.Fatalf("AgentCmd = %q, want the migrated command", p.AgentCmd())
	}
	if p.IsCouncil() {
		t.Fatalf("IsCouncil = true, want false for a migrated single agent")
	}
}

// TestMemberModelRoundTripsAndInjects verifies a {agent: preset, model} member
// survives Save/Load and resolves to the preset command WITH the model flag
// injected; an empty model omits the flag; a raw command is verbatim (model
// ignored). Covers each preset's model flag.
func TestMemberModelRoundTripsAndInjects(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()

	// A council: claude:opus + codex:o3 members, copilot:gpt-5 chair.
	members := []Member{{Agent: "claude", Model: "opus"}, {Agent: "codex", Model: "o3"}}
	chair := Member{Agent: "copilot", Model: "gpt-5"}
	if _, err := Add(file, root, []string{"."}, members, chair); err != nil {
		t.Fatalf("Add: %v", err)
	}

	list, err := List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("loaded %d, want 1", len(list))
	}
	p := list[0]
	// Structured fields round-trip.
	if len(p.Members) != 2 || p.Members[0].Agent != "claude" || p.Members[0].Model != "opus" ||
		p.Members[1].Agent != "codex" || p.Members[1].Model != "o3" {
		t.Fatalf("members = %+v, want claude:opus + codex:o3", p.Members)
	}
	if p.Chair == nil || p.Chair.Agent != "copilot" || p.Chair.Model != "gpt-5" {
		t.Fatalf("chair = %+v, want copilot:gpt-5", p.Chair)
	}
	// Resolved commands inject the model flag per preset.
	cmds := p.MemberCmds()
	wantClaude := "claude --model opus -p {prompt} --allowedTools mcp__outbox-md__*"
	wantCodex := "codex exec --dangerously-bypass-approvals-and-sandbox -m o3 {prompt}"
	wantCopilot := "copilot --model gpt-5 -p {prompt}"
	if len(cmds) != 2 || cmds[0] != wantClaude || cmds[1] != wantCodex {
		t.Fatalf("MemberCmds = %v, want [%q %q]", cmds, wantClaude, wantCodex)
	}
	if p.ChairCmd() != wantCopilot {
		t.Fatalf("ChairCmd = %q, want %q", p.ChairCmd(), wantCopilot)
	}

	// Empty model omits the flag: the plain preset command.
	noModel := Member{Agent: "claude"}
	if got := noModel.Command(); got != "claude -p {prompt} --allowedTools mcp__outbox-md__*" {
		t.Fatalf("no-model claude = %q, want the flagless preset", got)
	}
	// A raw command is verbatim; a stray model is ignored (user embedded their own).
	raw := Member{Agent: "my-agent --model x {prompt}", Model: "ignored"}
	if got := raw.Command(); got != "my-agent --model x {prompt}" {
		t.Fatalf("raw command = %q, want verbatim (model ignored)", got)
	}
}

// TestMemberUnmarshalStringOrObject verifies a member on disk loads from EITHER a
// bare string (legacy resolved command) or a {agent,model} object, and a legacy
// string chair loads too.
func TestMemberUnmarshalStringOrObject(t *testing.T) {
	file := regFile(t)
	raw := `[{"name":"app","root":"/work/app","docs":["."],` +
		`"members":["claude -p {prompt}",{"agent":"codex","model":"o3"}],` +
		`"chair":"claude -p {prompt}"}]`
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := Load(file)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := list[0]
	if len(p.Members) != 2 || p.Members[0].Agent != "claude -p {prompt}" || p.Members[0].Model != "" ||
		p.Members[1].Agent != "codex" || p.Members[1].Model != "o3" {
		t.Fatalf("members = %+v, want a raw string + a codex:o3 object", p.Members)
	}
	if p.Chair == nil || p.Chair.Agent != "claude -p {prompt}" {
		t.Fatalf("chair = %+v, want the legacy string chair", p.Chair)
	}
	// The codex object resolves with the model flag; the raw string is verbatim.
	if got := p.Members[1].Command(); got != "codex exec --dangerously-bypass-approvals-and-sandbox -m o3 {prompt}" {
		t.Fatalf("codex member command = %q", got)
	}
}

// TestUpdateReplacesFields verifies Update swaps a project's docs/members/chair
// (Name/Root immutable), enforces the council rule, and errors on an unknown name.
func TestUpdateReplacesFields(t *testing.T) {
	file := regFile(t)
	root := t.TempDir()
	for _, d := range []string{"specs", "rfcs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Add(file, root, []string{"specs"}, []Member{{Agent: "claude"}}, Member{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Promote to a council with a chair, and change the docs.
	up, err := Update(file, p.Name, []string{"specs", "rfcs"},
		[]Member{{Agent: "claude", Model: "opus"}, {Agent: "codex"}}, Member{Agent: "copilot"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.Root != p.Root || up.Name != p.Name {
		t.Fatalf("Update changed immutable name/root: %+v", up)
	}
	if len(up.Docs) != 2 || !up.IsCouncil() || up.ChairCmd() != "copilot -p {prompt}" {
		t.Fatalf("updated = %+v, want 2 docs + council + copilot chair", up)
	}
	if up.Members[0].Model != "opus" {
		t.Fatalf("member model not persisted: %+v", up.Members)
	}
	// Reload confirms persistence.
	list, _ := List(file)
	if len(list) != 1 || !list[0].IsCouncil() || len(list[0].Docs) != 2 {
		t.Fatalf("reloaded = %+v, want persisted council", list)
	}

	// A council update with no chair is rejected.
	if _, err := Update(file, p.Name, []string{"specs"}, []Member{{Agent: "a"}, {Agent: "b"}}, Member{}); !errors.Is(err, ErrChairRequired) {
		t.Fatalf("council-no-chair Update err = %v, want ErrChairRequired", err)
	}
	// An unknown project errors.
	if _, err := Update(file, "ghost", []string{"specs"}, nil, Member{}); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("unknown Update err = %v, want ErrProjectNotFound", err)
	}
}

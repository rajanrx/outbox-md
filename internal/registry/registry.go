// Package registry is the global list of projects outbox-md can serve. It is a
// small, pure, file-backed store: every function takes the registry file path as
// a parameter (the CLI resolves that path from the user config dir), so the core
// is trivially unit-testable against a temp file and never touches global state.
//
// A registry entry is a {name, root, docs, agents, chair} record. name is a short
// label (the project root's basename, disambiguated on collision); root is the
// absolute project repo root on disk; docs is a LIST of spec subpaths relative to
// root (each "." = the whole root) — a project serves the UNION of its docs
// subtrees; agents is the LIST of per-project agent commands (the council members)
// and chair is the command that synthesises the verdict. name is what the server
// routes disk writes by, so names MUST be unique — Add enforces that. The members
// are the commands the auto-reply engine spawns for THIS project (in root), letting
// different projects use different AIs.
//
// Council vs single-agent: a project with len(agents) <= 1 is single-agent mode
// exactly as today (the lone member — or, when empty, the global default — is the
// auto-reply agent; no chair). len(agents) >= 2 is council mode and REQUIRES a
// non-empty chair (Add enforces this at registration time).
//
// Back-compat: Load migrates older shapes so existing registries keep working;
// the new shape is written on the next Save. Tolerated on read:
//   - legacy {name, path}            → {name, root: path, docs: ["."]}
//   - single-string {docs: "x"}      → {docs: ["x"]}
//   - list          {docs:["a","b"]} → as-is
//   - a migration/legacy entry with no docs → {docs: ["."]}
//   - legacy single-string {agent: "x"} → {agents: ["x"]}
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project is one registered project outbox-md can serve.
type Project struct {
	Name string `json:"name"`
	// Root is the absolute project repo root on disk. It is the cwd the auto-reply
	// engine spawns the agent in (so the agent sees the repo's CLAUDE.md/.mcp.json/
	// codebase).
	Root string `json:"root"`
	// Docs is the list of spec subpaths relative to Root (each "." = the whole
	// root). The server serves the UNION of these subtrees; each imported doc is
	// keyed relative to Root, so the same filename under two subpaths never
	// collides. An empty list is normalised to ["."].
	Docs []string `json:"docs"`
	// Agents is the list of per-project agent command templates ({prompt} token) —
	// the council members. An empty list ⇒ the auto-reply engine falls back to the
	// global default command; a single entry is single-agent mode (that lone member
	// is the auto-reply agent); two or more entries is council mode, which requires
	// a non-empty Chair.
	Agents []string `json:"agents,omitempty"`
	// Chair is the command template ({prompt} token) that synthesises the council
	// verdict. Required (non-empty) when len(Agents) >= 2; unused (and empty) in
	// single-agent mode.
	Chair string `json:"chair,omitempty"`
}

// IsCouncil reports whether the project runs in council mode: two or more members,
// which (per Add's validation) always carries a chair.
func (p Project) IsCouncil() bool { return len(p.Members()) >= 2 }

// Members returns the project's agent commands (the council members), trimmed of
// empty entries. It is the normalised view the orchestration path iterates.
func (p Project) Members() []string {
	out := make([]string, 0, len(p.Agents))
	for _, a := range p.Agents {
		if strings.TrimSpace(a) != "" {
			out = append(out, a)
		}
	}
	return out
}

// AgentCmd returns the single-agent auto-reply command: the lone member (the first
// member if several are configured), or "" when no members are set (the project
// inherits the global default). This is what the existing single-agent auto-reply
// path consumes; council orchestration over Members() is wired separately.
func (p Project) AgentCmd() string {
	if m := p.Members(); len(m) > 0 {
		return m[0]
	}
	return ""
}

// SpecDirs is the set of directories whose .md files are served: Root joined
// with each docs entry. An empty (or absent) docs list defaults to ["."], so a
// project with no subpaths serves its whole root.
func (p Project) SpecDirs() []string {
	docs := p.Docs
	if len(docs) == 0 {
		docs = []string{"."}
	}
	dirs := make([]string, 0, len(docs))
	for _, d := range docs {
		if d == "" {
			d = "."
		}
		dirs = append(dirs, filepath.Join(p.Root, d))
	}
	return dirs
}

// DocRoots returns the project's docs subpaths RELATIVE to Root, normalised the
// same way SpecDirs normalises them (absent or "" → ["."]). These are the
// root-relative coverage keys the served predicate gates on — the sibling of
// SpecDirs, which joins the same list onto Root for import and file watching.
func (p Project) DocRoots() []string {
	docs := p.Docs
	if len(docs) == 0 {
		docs = []string{"."}
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		if strings.TrimSpace(d) == "" {
			d = "."
		}
		out = append(out, d)
	}
	return out
}

// UnmarshalJSON reads the current {name,root,docs:[…],agents:[…],chair} shape and
// tolerantly migrates older shapes so a registry written by an older outbox keeps
// working: legacy {name,path} → root ← path; single-string {docs:"x"} → docs ←
// ["x"]; an absent docs (legacy or migrated) → ["."]; legacy single-string
// {agent:"x"} → agents ← ["x"]. Empty docs entries within the list are normalised
// to ".". No council validation happens on read — Add owns that at registration.
func (p *Project) UnmarshalJSON(b []byte) error {
	var raw struct {
		Name   string          `json:"name"`
		Root   string          `json:"root"`
		Path   string          `json:"path"`  // legacy field
		Docs   json.RawMessage `json:"docs"`  // string OR []string OR absent
		Agent  string          `json:"agent"` // legacy single-agent field
		Agents []string        `json:"agents"`
		Chair  string          `json:"chair"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Name = raw.Name
	p.Root = raw.Root
	p.Chair = raw.Chair
	// Agents: prefer the new list; fall back to the legacy single-string `agent`
	// (migrated to a one-element list). Empty members are dropped.
	agents := raw.Agents
	if len(agents) == 0 && strings.TrimSpace(raw.Agent) != "" {
		agents = []string{raw.Agent}
	}
	cleanedAgents := make([]string, 0, len(agents))
	for _, a := range agents {
		if strings.TrimSpace(a) != "" {
			cleanedAgents = append(cleanedAgents, a)
		}
	}
	if len(cleanedAgents) > 0 {
		p.Agents = cleanedAgents
	} else {
		p.Agents = nil
	}
	// Legacy {name,path}: adopt path as the root when no root was recorded.
	if p.Root == "" && raw.Path != "" {
		p.Root = raw.Path
	}
	// Docs is tolerant: try a list first, then a bare string (the #59 single-string
	// shape), else surface a real error rather than silently dropping the entry.
	var docs []string
	if len(raw.Docs) > 0 {
		var list []string
		if err := json.Unmarshal(raw.Docs, &list); err == nil {
			docs = list
		} else {
			var single string
			if err := json.Unmarshal(raw.Docs, &single); err != nil {
				return fmt.Errorf("registry: docs must be a string or an array of strings: %w", err)
			}
			docs = []string{single}
		}
	}
	// Normalise empty entries to "." and default a missing/empty list to ["."].
	cleaned := make([]string, 0, len(docs))
	for _, d := range docs {
		if d == "" {
			d = "."
		}
		cleaned = append(cleaned, d)
	}
	if len(cleaned) == 0 {
		cleaned = []string{"."}
	}
	p.Docs = cleaned
	// Preserve a stored name (already unique in an older registry); only derive a
	// basename when the record carried none. Recomputing basename blindly would
	// collapse two disambiguated entries (docs, docs-2) back to one name.
	if p.Name == "" && p.Root != "" {
		p.Name = filepath.Base(p.Root)
	}
	return nil
}

// Load reads the registry from file. A missing file is not an error — it yields
// an empty list (the common first-run case). A present-but-malformed file is a
// real error the caller should surface rather than silently discard. Legacy
// {name,path} entries are migrated on read (see Project.UnmarshalJSON).
func Load(file string) ([]Project, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return []Project{}, nil
		}
		return nil, err
	}
	var projects []Project
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, fmt.Errorf("registry %s: invalid JSON: %w", file, err)
	}
	if projects == nil {
		projects = []Project{}
	}
	return projects, nil
}

// Save writes projects to file (pretty-printed, trailing newline), creating the
// parent directory if needed. It replaces the file wholesale, always in the new
// {name,root,docs,agents,chair} shape.
func Save(file string, projects []Project) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if projects == nil {
		projects = []Project{}
	}
	b, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(b, '\n'), 0o644)
}

// Add registers a project rooted at root, serving one or more docs subpaths
// (each relative to root; "." serves the whole root). root is resolved to an
// absolute path and MUST be an existing directory. At least ONE docs entry is
// required — "." is a valid, explicit entry (opt into a docs-only repo), so it
// counts, but the list may not be empty. Each docs entry must resolve to an
// existing directory UNDER root (traversal is rejected, on the symlink-resolved
// paths per entry); a single bad entry fails the whole add. Entries are cleaned
// and de-duplicated. agents are the per-project agent commands (the council
// members); chair is the verdict-synthesising command. Empty member entries are
// dropped. Council rule: with two or more members a non-empty chair is REQUIRED
// (an empty chair is a clear error); zero or one member is single-agent mode and
// needs no chair (zero members ⇒ the project inherits the global default at
// auto-reply time). Registration is idempotent by (root, docs-set): re-adding the
// same pair returns the existing entry unchanged. The entry's name is
// basename(root), disambiguated ("outbox-md", "outbox-md-2", …) when a DIFFERENT
// entry already holds that name, because the server routes disk writes by name.
func Add(file, root string, docs []string, agents []string, chair string) (Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Project{}, err
	}
	fi, err := os.Stat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return Project{}, fmt.Errorf("cannot add %q: no such directory", root)
		}
		return Project{}, err
	}
	if !fi.IsDir() {
		return Project{}, fmt.Errorf("cannot add %q: not a directory", root)
	}

	if len(docs) == 0 {
		return Project{}, fmt.Errorf("cannot add %q: at least one docs path is required (use \".\" for the whole repo)", root)
	}

	// Members + chair: drop empty member entries, then enforce the council rule.
	// Two or more members is council mode and requires a chair; zero or one member
	// is single-agent mode (zero ⇒ inherit the global default) and needs none.
	members := make([]string, 0, len(agents))
	for _, a := range agents {
		if strings.TrimSpace(a) != "" {
			members = append(members, a)
		}
	}
	chair = strings.TrimSpace(chair)
	if len(members) >= 2 && chair == "" {
		return Project{}, fmt.Errorf("cannot add %q: council mode (%d members) requires a chair — pass --chair <preset> or --chair-cmd \"<cmd>\"", root, len(members))
	}
	// Resolve root's symlinks ONCE; each docs entry is checked against it.
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return Project{}, err
	}

	cleaned := make([]string, 0, len(docs))
	seen := map[string]bool{}
	for _, d := range docs {
		d = strings.TrimSpace(d)
		if d == "" {
			d = "."
		}
		d = filepath.Clean(d)
		if filepath.IsAbs(d) {
			return Project{}, fmt.Errorf("cannot add: docs %q must be relative to the project root", d)
		}
		specDir := filepath.Join(absRoot, d)
		sfi, err := os.Stat(specDir)
		if err != nil {
			if os.IsNotExist(err) {
				return Project{}, fmt.Errorf("cannot add: docs %q is not a directory under %s", d, absRoot)
			}
			return Project{}, err
		}
		if !sfi.IsDir() {
			return Project{}, fmt.Errorf("cannot add: docs %q is not a directory", d)
		}
		// Containment on the RESOLVED paths, not just lexically: a symlinked
		// component in docs (or in root) can escape root while passing a ../ check.
		// EvalSymlinks BOTH sides — resolving only one false-positives on macOS
		// /tmp→/private/tmp.
		resolvedSpec, err := filepath.EvalSymlinks(specDir)
		if err != nil {
			return Project{}, err
		}
		rel, err := filepath.Rel(resolvedRoot, resolvedSpec)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return Project{}, fmt.Errorf("cannot add: docs %q escapes the project root", d)
		}
		if !seen[d] {
			seen[d] = true
			cleaned = append(cleaned, d)
		}
	}

	projects, err := Load(file)
	if err != nil {
		return Project{}, err
	}
	// Dedupe by (root, docs-set): the same project already registered → return it
	// unchanged (idempotent), even if agentCmd differs, so a re-add never
	// duplicates.
	for _, p := range projects {
		if p.Root == absRoot && equalDocs(p.Docs, cleaned) {
			return p, nil
		}
	}

	name := uniqueName(filepath.Base(absRoot), projects)
	var storedAgents []string
	if len(members) > 0 {
		storedAgents = members
	}
	p := Project{Name: name, Root: absRoot, Docs: cleaned, Agents: storedAgents, Chair: chair}
	projects = append(projects, p)
	if err := Save(file, projects); err != nil {
		return Project{}, err
	}
	return p, nil
}

// equalDocs reports whether two cleaned docs lists are the same SET — order does
// not matter, so `add root a b` then `add root b a` is idempotent, not a dup.
func equalDocs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// uniqueName returns base, or base-2/base-3/… when base is already taken by an
// existing project, so no two entries ever share a name.
func uniqueName(base string, existing []Project) string {
	taken := map[string]bool{}
	for _, p := range existing {
		taken[p.Name] = true
	}
	if base == "" {
		base = "project"
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// Remove unregisters every project whose name OR root matches ref (ref is
// resolved to an absolute path before the root comparison, so a relative path
// works too; the raw ref is also compared so a name still matches). It returns
// whether anything was removed. Removing an unknown ref is not an error — it
// simply removes nothing.
func Remove(file, ref string) (bool, error) {
	projects, err := Load(file)
	if err != nil {
		return false, err
	}
	absRef := ref
	if a, err := filepath.Abs(ref); err == nil {
		absRef = a
	}
	kept := make([]Project, 0, len(projects))
	removed := false
	for _, p := range projects {
		if p.Name == ref || p.Root == ref || p.Root == absRef {
			removed = true
			continue
		}
		kept = append(kept, p)
	}
	if !removed {
		return false, nil
	}
	if err := Save(file, kept); err != nil {
		return false, err
	}
	return true, nil
}

// DocRemoval identifies one docs entry to remove: a project by name and one of
// its docs subpaths. It is the unit the interactive `outbox remove` multiselect
// collects (one per ticked row) and the key ApplyRemovals matches on.
type DocRemoval struct {
	Project string
	Docs    string
}

// ApplyRemovals removes each requested docs entry from its project (matched by
// project name AND docs subpath) and returns the resulting project slice plus the
// subset of removals actually applied (for reporting). A project whose docs list
// becomes empty after removals is dropped entirely. It is pure — no file I/O and
// no mutation of the input slice — so the multiselect's selection→mutation logic
// is unit-testable without a terminal.
//
// Batch-safe: all removals for a project are collected first, then applied in one
// pass, so removing several docs from one project (or the last docs of one project
// while trimming another) drops/keeps each project correctly.
func ApplyRemovals(projects []Project, removals []DocRemoval) ([]Project, []DocRemoval) {
	// Index the requested docs to remove per project name.
	toRemove := make(map[string]map[string]bool, len(removals))
	for _, r := range removals {
		if toRemove[r.Project] == nil {
			toRemove[r.Project] = map[string]bool{}
		}
		toRemove[r.Project][r.Docs] = true
	}

	kept := make([]Project, 0, len(projects))
	applied := make([]DocRemoval, 0, len(removals))
	for _, p := range projects {
		drop, ok := toRemove[p.Name]
		if !ok {
			kept = append(kept, p)
			continue
		}
		docs := p.Docs
		if len(docs) == 0 {
			docs = []string{"."}
		}
		remaining := make([]string, 0, len(docs))
		for _, d := range docs {
			if d == "" {
				d = "."
			}
			if drop[d] {
				applied = append(applied, DocRemoval{Project: p.Name, Docs: d})
				continue
			}
			remaining = append(remaining, d)
		}
		// A project whose every docs entry was removed is dropped entirely.
		if len(remaining) == 0 {
			continue
		}
		p.Docs = remaining
		kept = append(kept, p)
	}
	return kept, applied
}

// List returns the registered projects (equivalent to Load).
func List(file string) ([]Project, error) { return Load(file) }

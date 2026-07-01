// Package registry is the global list of projects outbox-md can serve. It is a
// small, pure, file-backed store: every function takes the registry file path as
// a parameter (the CLI resolves that path from the user config dir), so the core
// is trivially unit-testable against a temp file and never touches global state.
//
// A registry entry is a {name, root, docs, agent} record. name is a short label
// (the project root's basename, disambiguated on collision); root is the absolute
// project repo root on disk; docs is a LIST of spec subpaths relative to root
// (each "." = the whole root) — a project serves the UNION of its docs subtrees;
// agent is an optional per-project agent command (empty ⇒ use the global
// default). name is what the server routes disk writes by, so names MUST be
// unique — Add enforces that. agent is the command the auto-reply engine spawns
// for THIS project (in root), letting different projects use different AIs.
//
// Back-compat: Load migrates older shapes so existing registries keep working;
// the new list shape is written on the next Save. Tolerated on read:
//   - legacy {name, path}            → {name, root: path, docs: ["."]}
//   - single-string {docs: "x"}      → {docs: ["x"]}
//   - list          {docs:["a","b"]} → as-is
//   - a migration/legacy entry with no docs → {docs: ["."]}
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// Agent is the optional per-project agent command template ({prompt} token).
	// Empty ⇒ the auto-reply engine falls back to the global default command.
	Agent string `json:"agent,omitempty"`
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

// UnmarshalJSON reads the current {name,root,docs:[…],agent} shape and tolerantly
// migrates older shapes so a registry written by an older outbox keeps working:
// legacy {name,path} → root ← path; single-string {docs:"x"} → docs ← ["x"]; an
// absent docs (legacy or migrated) → ["."]. Empty entries within the list are
// normalised to ".".
func (p *Project) UnmarshalJSON(b []byte) error {
	var raw struct {
		Name  string          `json:"name"`
		Root  string          `json:"root"`
		Path  string          `json:"path"` // legacy field
		Docs  json.RawMessage `json:"docs"` // string OR []string OR absent
		Agent string          `json:"agent"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Name = raw.Name
	p.Root = raw.Root
	p.Agent = raw.Agent
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
// {name,root,docs,agent} shape.
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
// and de-duplicated. agentCmd, when non-empty, is stored as this project's
// per-project agent command. Registration is idempotent by (root, docs-set):
// re-adding the same pair returns the existing entry unchanged. The entry's name
// is basename(root), disambiguated ("outbox-md", "outbox-md-2", …) when a
// DIFFERENT entry already holds that name, because the server routes disk writes
// by name.
func Add(file, root string, docs []string, agentCmd string) (Project, error) {
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
	p := Project{Name: name, Root: absRoot, Docs: cleaned, Agent: agentCmd}
	projects = append(projects, p)
	if err := Save(file, projects); err != nil {
		return Project{}, err
	}
	return p, nil
}

// equalDocs reports whether two cleaned docs lists are identical (same entries in
// the same order) — the idempotency key alongside root.
func equalDocs(a, b []string) bool {
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

// List returns the registered projects (equivalent to Load).
func List(file string) ([]Project, error) { return Load(file) }

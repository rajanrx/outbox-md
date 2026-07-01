// Package registry is the global list of projects outbox-md can serve. It is a
// small, pure, file-backed store: every function takes the registry file path as
// a parameter (the CLI resolves that path from the user config dir), so the core
// is trivially unit-testable against a temp file and never touches global state.
//
// A registry entry is a {name, root, docs, agent} record. name is a short label
// (the project root's basename, disambiguated on collision); root is the absolute
// project repo root on disk; docs is the spec subpath relative to root (default
// "." = serve the whole root); agent is an optional per-project agent command
// (empty ⇒ use the global default). The pair (name → root/docs) is what the
// server uses to route a project's docs and disk writes, so names MUST be
// unique — Add enforces that. agent is the command the auto-reply engine spawns
// for THIS project (in root), letting different projects use different AIs.
//
// Back-compat: an older entry was {name, path}. Load migrates it in-place to
// {name: name (== basename(path)), root: path, docs: ".", agent: ""} so existing
// registries keep working; the new shape is written on the next Save.
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
	// Docs is the spec subpath relative to Root ("." = the whole root). The server
	// serves, imports, and write-routes filepath.Join(Root, Docs).
	Docs string `json:"docs"`
	// Agent is the optional per-project agent command template ({prompt} token).
	// Empty ⇒ the auto-reply engine falls back to the global default command.
	Agent string `json:"agent,omitempty"`
}

// SpecDir is the directory whose .md files are served: Root joined with Docs.
// Docs defaults to "." so a project with no subpath serves its whole root.
func (p Project) SpecDir() string {
	docs := p.Docs
	if docs == "" {
		docs = "."
	}
	return filepath.Join(p.Root, docs)
}

// UnmarshalJSON reads either the current {name,root,docs,agent} shape or the
// legacy {name,path} shape, migrating the latter: root ← path, docs ← "." when
// absent. It is tolerant so a registry written by an older outbox keeps working.
func (p *Project) UnmarshalJSON(b []byte) error {
	var raw struct {
		Name  string `json:"name"`
		Root  string `json:"root"`
		Path  string `json:"path"` // legacy field
		Docs  string `json:"docs"`
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Name = raw.Name
	p.Root = raw.Root
	p.Docs = raw.Docs
	p.Agent = raw.Agent
	// Legacy {name,path}: adopt path as the root when no root was recorded.
	if p.Root == "" && raw.Path != "" {
		p.Root = raw.Path
	}
	if p.Docs == "" {
		p.Docs = "."
	}
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

// Add registers a project rooted at root, serving the docs subpath (relative to
// root; "." serves the whole root). root is resolved to an absolute path and
// must be an existing directory; docs must resolve to an existing directory
// UNDER root (traversal is rejected). agentCmd, when non-empty, is stored as this
// project's per-project agent command. Registration is idempotent by (root,docs):
// re-adding the same pair returns the existing entry unchanged. The entry's name
// is basename(root), disambiguated ("outbox-md", "outbox-md-2", …) when a
// DIFFERENT entry already holds that name, because the server routes disk writes
// by name.
func Add(file, root, docs, agentCmd string) (Project, error) {
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

	docs = strings.TrimSpace(docs)
	if docs == "" {
		docs = "."
	}
	docs = filepath.Clean(docs)
	// Reject any docs that escapes root (absolute path or ../ traversal), then
	// require the resolved spec dir to be an existing directory.
	if filepath.IsAbs(docs) {
		return Project{}, fmt.Errorf("cannot add: docs %q must be relative to the project root", docs)
	}
	specDir := filepath.Join(absRoot, docs)
	rel, err := filepath.Rel(absRoot, specDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return Project{}, fmt.Errorf("cannot add: docs %q escapes the project root", docs)
	}
	sfi, err := os.Stat(specDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Project{}, fmt.Errorf("cannot add: docs %q is not a directory under %s", docs, absRoot)
		}
		return Project{}, err
	}
	if !sfi.IsDir() {
		return Project{}, fmt.Errorf("cannot add: docs %q is not a directory", docs)
	}

	projects, err := Load(file)
	if err != nil {
		return Project{}, err
	}
	// Dedupe by (root, docs): the same project already registered → return it
	// unchanged (idempotent), even if agentCmd differs, so a re-add never
	// duplicates.
	for _, p := range projects {
		if p.Root == absRoot && p.Docs == docs {
			return p, nil
		}
	}

	name := uniqueName(filepath.Base(absRoot), projects)
	p := Project{Name: name, Root: absRoot, Docs: docs, Agent: agentCmd}
	projects = append(projects, p)
	if err := Save(file, projects); err != nil {
		return Project{}, err
	}
	return p, nil
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

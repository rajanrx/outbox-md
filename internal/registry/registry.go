// Package registry is the global list of projects outbox-md can serve. It is a
// small, pure, file-backed store: every function takes the registry file path as
// a parameter (the CLI resolves that path from the user config dir), so the core
// is trivially unit-testable against a temp file and never touches global state.
//
// A registry entry is a {name, path} pair. name is a short label (the folder's
// basename, disambiguated on collision); path is the absolute folder on disk. The
// pair (name → path) is what the server uses to route a project's docs and disk
// writes, so names MUST be unique — Add enforces that.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Project is one registered folder outbox-md can serve.
type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Load reads the registry from file. A missing file is not an error — it yields
// an empty list (the common first-run case). A present-but-malformed file is a
// real error the caller should surface rather than silently discard.
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
// parent directory if needed. It replaces the file wholesale.
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

// Add registers dir as a project. It resolves dir to an absolute path and
// requires it to be an existing directory (a missing path or a file is an
// error — the most common mistake). Registration is idempotent by PATH: adding
// an already-registered folder returns the existing entry and does not duplicate
// it. The entry's name is the folder basename, disambiguated ("docs", "docs-2",
// …) when a DIFFERENT path already holds that name, because the server routes
// disk writes by name — a name collision across two folders would misroute a
// write, so names are kept unique.
func Add(file, dir string) (Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Project{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Project{}, fmt.Errorf("cannot add %q: no such directory", dir)
		}
		return Project{}, err
	}
	if !fi.IsDir() {
		return Project{}, fmt.Errorf("cannot add %q: not a directory", dir)
	}

	projects, err := Load(file)
	if err != nil {
		return Project{}, err
	}
	// Dedupe by path: already registered → return the existing entry unchanged.
	for _, p := range projects {
		if p.Path == abs {
			return p, nil
		}
	}

	name := uniqueName(filepath.Base(abs), projects)
	p := Project{Name: name, Path: abs}
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

// Remove unregisters every project whose name OR path matches ref (ref is
// resolved to an absolute path before the path comparison, so a relative path
// works too). It returns whether anything was removed. Removing an unknown ref
// is not an error — it simply removes nothing.
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
		if p.Name == ref || p.Path == ref || p.Path == absRef {
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

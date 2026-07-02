// Package registry is the global list of projects outbox-md can serve. It is a
// small, pure, file-backed store: every function takes the registry file path as
// a parameter (the CLI resolves that path from the user config dir), so the core
// is trivially unit-testable against a temp file and never touches global state.
//
// A registry entry is a {name, root, docs, members, chair} record. name is a short
// label (the project root's basename, disambiguated on collision); root is the
// absolute project repo root on disk; docs is a LIST of spec subpaths relative to
// root (each "." = the whole root) — a project serves the UNION of its docs
// subtrees; members is the LIST of per-project council members and chair is the
// member that synthesises the verdict. name is what the server routes disk writes
// by, so names MUST be unique — Add enforces that.
//
// A member is a {agent, model} record: agent is a preset NAME (claude/codex/
// copilot) OR a raw command template (with a {prompt} token), and model is an
// optional per-member model string. The resolved spawn command is Member.Command:
// a preset resolves through agentpreset (injecting the model flag when set); a raw
// command is used verbatim (model ignored — the user embedded it themselves). The
// engine consumes the RESOLVED command strings (Project.MemberCmds / ChairCmd), so
// orchestration never sees the structured member — only its resolved command.
//
// Council vs single-agent: a project with len(MemberCmds) <= 1 is single-agent
// mode exactly as today (the lone member — or, when empty, the global default — is
// the auto-reply agent; no chair). len(MemberCmds) >= 2 is council mode and
// REQUIRES a non-empty chair (Add/Update enforce this at registration time).
//
// Back-compat: Load migrates older shapes so existing registries keep working;
// the new shape is written on the next Save. Tolerated on read:
//   - legacy {name, path}                     → {name, root: path, docs: ["."]}
//   - single-string {docs: "x"}               → {docs: ["x"]}
//   - list          {docs:["a","b"]}          → as-is
//   - a migration/legacy entry with no docs   → {docs: ["."]}
//   - legacy single-string {agent: "x"}       → {members: [{agent: "x"}]}
//   - legacy {agents: ["a","b"]} (resolved)   → {members: [{agent:"a"},{agent:"b"}]}
//   - a member element that is a bare string  → {agent: "<string>"}
//   - legacy string {chair: "cmd"}            → {chair: {agent: "cmd"}}
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rajanrx/outbox-md/internal/agentpreset"
)

// Validation sentinels, so callers (the HTTP API) can map a registry error to the
// right status code with errors.Is rather than string-matching.
var (
	// ErrRootNotDir is returned when a project root is missing or not a directory.
	ErrRootNotDir = errors.New("project root must be an existing directory")
	// ErrNoDocs is returned when no docs path is supplied.
	ErrNoDocs = errors.New("at least one docs path is required")
	// ErrBadDocs is returned when a docs entry is absolute, missing, not a
	// directory, or escapes the project root.
	ErrBadDocs = errors.New("invalid docs path")
	// ErrChairRequired is returned when a council (>=2 members) has no chair.
	ErrChairRequired = errors.New("council mode (>=2 members) requires a chair")
	// ErrProjectNotFound is returned by Update for an unknown project name.
	ErrProjectNotFound = errors.New("no such project")
)

// Member is one council member (or the chair): a preset name or raw command in
// Agent, plus an optional per-member Model. Its resolved spawn command is
// Command(). Empty (Agent == "") members are dropped at registration.
type Member struct {
	// Agent is a preset name (claude/codex/copilot) OR a raw command template
	// carrying a {prompt} token. A known preset name resolves through agentpreset
	// (with the model flag injected when Model is set); anything else is treated as
	// a raw command and used verbatim.
	Agent string `json:"agent"`
	// Model is the optional per-member model string. It is injected into a preset's
	// command (e.g. `claude --model <model> …`) and IGNORED for a raw Agent command.
	Model string `json:"model,omitempty"`
}

// Command returns the resolved spawn command for the member: a known preset
// resolves through agentpreset (with the model flag injected when Model is set);
// a raw Agent command is returned verbatim (its model is the user's business).
// An empty Agent yields "".
func (m Member) Command() string {
	agent := strings.TrimSpace(m.Agent)
	if agent == "" {
		return ""
	}
	if cmd, ok := agentpreset.ResolveModel(agent, m.Model); ok {
		return cmd
	}
	return agent
}

// UnmarshalJSON accepts a member as EITHER a bare string (a raw command → {agent:
// string}, the legacy resolved-command shape) or a {agent, model} object.
func (m *Member) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		m.Agent = strings.TrimSpace(s)
		m.Model = ""
		return nil
	}
	var obj struct {
		Agent string `json:"agent"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("registry: member must be a string or {agent,model}: %w", err)
	}
	m.Agent = strings.TrimSpace(obj.Agent)
	m.Model = strings.TrimSpace(obj.Model)
	return nil
}

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
	// Members is the list of per-project council members ({agent, model}). An empty
	// list ⇒ the auto-reply engine falls back to the global default command; a
	// single member is single-agent mode (its resolved command is the auto-reply
	// agent); two or more members is council mode, which requires a non-nil Chair.
	Members []Member `json:"members,omitempty"`
	// Chair is the member that synthesises the council verdict. Required (non-nil)
	// when len(MemberCmds) >= 2; nil (and unused) in single-agent mode.
	Chair *Member `json:"chair,omitempty"`
}

// IsCouncil reports whether the project runs in council mode: two or more members,
// which (per Add's validation) always carries a chair.
func (p Project) IsCouncil() bool { return len(p.MemberCmds()) >= 2 }

// MemberCmds returns the project's RESOLVED member commands (the council members),
// with empty entries dropped. It is the normalised view the orchestration path
// iterates — each member's preset+model is resolved to its spawn command here.
func (p Project) MemberCmds() []string {
	out := make([]string, 0, len(p.Members))
	for _, m := range p.Members {
		if c := m.Command(); strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

// AgentCmd returns the single-agent auto-reply command: the lone member's resolved
// command (the first member if several are configured), or "" when no members are
// set (the project inherits the global default). This is what the existing
// single-agent auto-reply path consumes; council orchestration over MemberCmds is
// wired separately.
func (p Project) AgentCmd() string {
	if m := p.MemberCmds(); len(m) > 0 {
		return m[0]
	}
	return ""
}

// ChairCmd returns the chair's resolved spawn command, or "" when no chair is set.
func (p Project) ChairCmd() string {
	if p.Chair == nil {
		return ""
	}
	return p.Chair.Command()
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

// UnmarshalJSON reads the current {name,root,docs:[…],members:[…],chair:{…}} shape
// and tolerantly migrates older shapes so a registry written by an older outbox
// keeps working: legacy {name,path} → root ← path; single-string {docs:"x"} →
// docs ← ["x"]; an absent docs (legacy or migrated) → ["."]; legacy single-string
// {agent:"x"} → members ← [{agent:"x"}]; legacy {agents:[…]} (resolved commands) →
// members ← [{agent:cmd},…]; a bare-string member → {agent:string}; legacy string
// {chair:"cmd"} → chair ← {agent:"cmd"}. Empty docs/member entries are normalised.
// No council validation happens on read — Add/Update own that at registration.
func (p *Project) UnmarshalJSON(b []byte) error {
	var raw struct {
		Name    string          `json:"name"`
		Root    string          `json:"root"`
		Path    string          `json:"path"`   // legacy field
		Docs    json.RawMessage `json:"docs"`    // string OR []string OR absent
		Agent   string          `json:"agent"`   // legacy single-agent field (resolved cmd)
		Agents  []string        `json:"agents"`  // legacy member list (resolved cmds)
		Members []Member        `json:"members"` // current shape ({agent,model} or bare string)
		Chair   json.RawMessage `json:"chair"`   // string (legacy) OR {agent,model}
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.Name = raw.Name
	p.Root = raw.Root
	// Members: prefer the new structured list; fall back to the legacy resolved
	// `agents` list, then the even-older single-string `agent`. Each legacy entry is
	// a raw command, so it becomes a Member with that Agent and no model.
	var members []Member
	switch {
	case len(raw.Members) > 0:
		members = raw.Members
	case len(raw.Agents) > 0:
		for _, a := range raw.Agents {
			members = append(members, Member{Agent: a})
		}
	case strings.TrimSpace(raw.Agent) != "":
		members = []Member{{Agent: raw.Agent}}
	}
	cleaned := make([]Member, 0, len(members))
	for _, m := range members {
		m.Agent = strings.TrimSpace(m.Agent)
		m.Model = strings.TrimSpace(m.Model)
		if m.Agent != "" {
			cleaned = append(cleaned, m)
		}
	}
	if len(cleaned) > 0 {
		p.Members = cleaned
	} else {
		p.Members = nil
	}
	// Chair: a bare string (legacy resolved cmd) OR a {agent,model} object, via
	// Member.UnmarshalJSON. An empty agent leaves the chair unset.
	if len(raw.Chair) > 0 {
		var c Member
		if err := json.Unmarshal(raw.Chair, &c); err != nil {
			return err
		}
		if strings.TrimSpace(c.Agent) != "" {
			c.Agent = strings.TrimSpace(c.Agent)
			c.Model = strings.TrimSpace(c.Model)
			p.Chair = &c
		}
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
	cleanedDocs := make([]string, 0, len(docs))
	for _, d := range docs {
		if d == "" {
			d = "."
		}
		cleanedDocs = append(cleanedDocs, d)
	}
	if len(cleanedDocs) == 0 {
		cleanedDocs = []string{"."}
	}
	p.Docs = cleanedDocs
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
// {name,root,docs,members,chair} shape.
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

// normalizeMembers drops empty member entries and enforces the council rule: two
// or more members require a non-empty chair. It returns the cleaned member slice
// (nil when empty), the chair as a pointer (nil when empty), or ErrChairRequired.
func normalizeMembers(members []Member, chair Member) ([]Member, *Member, error) {
	clean := make([]Member, 0, len(members))
	for _, m := range members {
		m.Agent = strings.TrimSpace(m.Agent)
		m.Model = strings.TrimSpace(m.Model)
		if m.Agent != "" {
			clean = append(clean, m)
		}
	}
	chair.Agent = strings.TrimSpace(chair.Agent)
	chair.Model = strings.TrimSpace(chair.Model)
	var chairPtr *Member
	if chair.Agent != "" {
		c := chair
		chairPtr = &c
	}
	if len(clean) >= 2 && chairPtr == nil {
		return nil, nil, ErrChairRequired
	}
	if len(clean) == 0 {
		clean = nil
	}
	return clean, chairPtr, nil
}

// validateDocs cleans and validates a docs list against an absolute project root,
// returning the deduped cleaned list. Every entry must resolve to an existing
// directory UNDER root (traversal — lexical or via a symlink — is rejected on the
// symlink-resolved paths); a single bad entry fails the whole call. An empty list
// is an error (ErrNoDocs) — "." is a valid, explicit entry.
func validateDocs(absRoot string, docs []string) ([]string, error) {
	if len(docs) == 0 {
		return nil, fmt.Errorf("%w (use \".\" for the whole repo)", ErrNoDocs)
	}
	// Resolve root's symlinks ONCE; each docs entry is checked against it.
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
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
			return nil, fmt.Errorf("%w: %q must be relative to the project root", ErrBadDocs, d)
		}
		specDir := filepath.Join(absRoot, d)
		sfi, err := os.Stat(specDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("%w: %q is not a directory under %s", ErrBadDocs, d, absRoot)
			}
			return nil, err
		}
		if !sfi.IsDir() {
			return nil, fmt.Errorf("%w: %q is not a directory", ErrBadDocs, d)
		}
		// Containment on the RESOLVED paths, not just lexically: a symlinked
		// component in docs (or in root) can escape root while passing a ../ check.
		// EvalSymlinks BOTH sides — resolving only one false-positives on macOS
		// /tmp→/private/tmp.
		resolvedSpec, err := filepath.EvalSymlinks(specDir)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(resolvedRoot, resolvedSpec)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return nil, fmt.Errorf("%w: %q escapes the project root", ErrBadDocs, d)
		}
		if !seen[d] {
			seen[d] = true
			cleaned = append(cleaned, d)
		}
	}
	return cleaned, nil
}

// Add registers a project rooted at root, serving one or more docs subpaths
// (each relative to root; "." serves the whole root). root is resolved to an
// absolute path and MUST be an existing directory. At least ONE docs entry is
// required — "." is a valid, explicit entry (opt into a docs-only repo), so it
// counts, but the list may not be empty. Each docs entry must resolve to an
// existing directory UNDER root (traversal is rejected). Entries are cleaned and
// de-duplicated. members are the per-project council members; chair is the
// verdict-synthesising member. Empty member entries are dropped. Council rule:
// with two or more members a non-empty chair is REQUIRED (ErrChairRequired); zero
// or one member is single-agent mode and needs no chair (zero members ⇒ the
// project inherits the global default at auto-reply time). Registration is
// idempotent by (root, docs-set): re-adding the same pair returns the existing
// entry unchanged. The entry's name is basename(root), disambiguated ("outbox-md",
// "outbox-md-2", …) when a DIFFERENT entry already holds that name.
func Add(file, root string, docs []string, members []Member, chair Member) (Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Project{}, err
	}
	fi, err := os.Stat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return Project{}, fmt.Errorf("%w: %q does not exist", ErrRootNotDir, root)
		}
		return Project{}, err
	}
	if !fi.IsDir() {
		return Project{}, fmt.Errorf("%w: %q is a file", ErrRootNotDir, root)
	}

	cleanMembers, chairPtr, err := normalizeMembers(members, chair)
	if err != nil {
		return Project{}, err
	}
	cleaned, err := validateDocs(absRoot, docs)
	if err != nil {
		return Project{}, err
	}

	projects, err := Load(file)
	if err != nil {
		return Project{}, err
	}
	// Dedupe by (root, docs-set): the same project already registered → return it
	// unchanged (idempotent), even if members differ, so a re-add never duplicates.
	for _, p := range projects {
		if p.Root == absRoot && equalDocs(p.Docs, cleaned) {
			return p, nil
		}
	}

	name := uniqueName(filepath.Base(absRoot), projects)
	p := Project{Name: name, Root: absRoot, Docs: cleaned, Members: cleanMembers, Chair: chairPtr}
	projects = append(projects, p)
	if err := Save(file, projects); err != nil {
		return Project{}, err
	}
	return p, nil
}

// Update replaces the docs, members and chair of the project named name (Name and
// Root are immutable). docs is re-validated against the project's existing Root;
// members/chair go through the same council rule as Add. An unknown name returns
// ErrProjectNotFound. It is a full-field replace of the three mutable fields — the
// HTTP layer computes the desired state (merging a PATCH onto the existing project)
// before calling this.
func Update(file, name string, docs []string, members []Member, chair Member) (Project, error) {
	projects, err := Load(file)
	if err != nil {
		return Project{}, err
	}
	idx := -1
	for i, p := range projects {
		if p.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return Project{}, fmt.Errorf("%w: %q", ErrProjectNotFound, name)
	}
	proj := projects[idx]

	cleanMembers, chairPtr, err := normalizeMembers(members, chair)
	if err != nil {
		return Project{}, err
	}
	cleaned, err := validateDocs(proj.Root, docs)
	if err != nil {
		return Project{}, err
	}
	proj.Docs = cleaned
	proj.Members = cleanMembers
	proj.Chair = chairPtr
	projects[idx] = proj
	if err := Save(file, projects); err != nil {
		return Project{}, err
	}
	return proj, nil
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

// Package git provides read-only access to the git working tree that contains
// the served data directory. It never writes, stages, or commits — it only
// detects whether the served folder is inside a work tree and, on request,
// renders a unified diff of the changed *.md files (working tree vs HEAD).
//
// go-git is used throughout: the runtime image is distroless and has no git
// binary, so shelling out is not an option.
package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// ctx keeps the frontend renderer and this builder in lockstep: it mirrors the
// context/gap collapsing in web/src/suggestion/diff.ts.
const ctxLines = 3

// maxFileBytes caps how large a file we will diff. Anything bigger is skipped
// (omitted from the result) rather than loaded — a spec folder holds prose, not
// large blobs, and huge/binary files would only bloat the payload.
const maxFileBytes = 1 << 20 // 1 MiB

// Row mirrors web/src/suggestion/diff.ts `Row` so the frontend renders a
// server-built diff with the exact same styling as the single-file diff.
type Row struct {
	Op   string `json:"op"` // "eq" | "ins" | "del" | "gap"
	Text string `json:"text"`
}

// FileDiff is one changed .md file and its collapsed unified-diff rows. Path is
// relative to the served directory (not the repo root), so it matches the path
// a document is keyed on everywhere else in the UI.
type FileDiff struct {
	Path string `json:"path"`
	Rows []Row  `json:"rows"`
}

// Result is the /api/git/diff payload. Enabled is false when the served folder
// is not inside a git work tree; Files is then empty.
type Result struct {
	Enabled bool       `json:"enabled"`
	Files   []FileDiff `json:"files"`
}

// Service is read-only git access scoped to a single served directory. A nil
// *Service is valid and behaves as "no git" — all methods are nil-safe so the
// API can be constructed without a git service in tests.
type Service struct {
	dir    string // the served directory (OUTBOX_DIR)
	hasGit bool
}

// Open detects, once at startup, whether dir sits inside a git work tree. It
// walks up for a .git (DetectDotGit) and never fails: a non-repo dir yields a
// service whose HasGit() is false.
func Open(dir string) *Service {
	s := &Service{dir: dir}
	if _, err := gogit.PlainOpenWithOptions(dir, &gogit.PlainOpenOptions{DetectDotGit: true}); err == nil {
		s.hasGit = true
	}
	return s
}

// HasGit reports whether the served folder is inside a git work tree.
func (s *Service) HasGit() bool { return s != nil && s.hasGit }

// Diff returns the working-tree-vs-HEAD diff of every changed *.md file within
// the served directory. It is best-effort and bounded by ctx: on any error,
// timeout, or a non-git dir it returns a well-formed Result rather than failing.
func (s *Service) Diff(ctx context.Context) Result {
	if !s.HasGit() {
		return Result{Enabled: false, Files: []FileDiff{}}
	}
	type out struct{ files []FileDiff }
	done := make(chan out, 1)
	go func() {
		// go-git can panic on a corrupt/unusual repo. Recover HERE — the request
		// goroutine's recover cannot catch a panic on this child goroutine — so a
		// bad repo degrades to an empty diff instead of crashing the server.
		defer func() {
			if recover() != nil {
				done <- out{files: nil}
			}
		}()
		files := s.build()
		done <- out{files: files}
	}()
	select {
	case <-ctx.Done():
		// Never hang: the repo is enabled, we just could not finish in time.
		return Result{Enabled: true, Files: []FileDiff{}}
	case r := <-done:
		if r.files == nil {
			r.files = []FileDiff{}
		}
		return Result{Enabled: true, Files: r.files}
	}
}

// build does the actual enumeration. It opens the repo fresh (read-only),
// enumerates changed paths, and for each candidate .md within the served dir
// compares HEAD content against on-disk content. Enumerating by content —
// old := HEAD blob (empty if absent), new := on-disk (empty if absent) — covers
// added, deleted, staged, and unstaged changes uniformly.
func (s *Service) build() []FileDiff {
	repo, err := gogit.PlainOpenWithOptions(s.dir, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil
	}
	repoRoot := wt.Filesystem.Root()

	// prefix is the served dir relative to the repo root, in slash form. "" (or
	// ".") means the served dir IS the repo root. Resolve symlinks on both first:
	// go-git reports the real (resolved) root while the served dir may still carry
	// a symlink (e.g. macOS /var → /private/var), and an unresolved mismatch would
	// make filepath.Rel escape with ".." and filter out every file.
	prefix := ""
	if rel, err := filepath.Rel(evalSymlinks(repoRoot), evalSymlinks(s.dir)); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel)
	}

	status, err := wt.Status()
	if err != nil {
		return nil
	}

	// HEAD content lookup (all-absent in a repo with no commits — then every
	// changed file reads as newly added).
	headContent := s.headResolver(repo)

	var files []FileDiff
	for path := range status {
		p := filepath.ToSlash(path)
		if !strings.HasSuffix(strings.ToLower(p), ".md") {
			continue
		}
		if !withinPrefix(p, prefix) {
			continue
		}
		oldC, oldPresent, oldSkip := headContent(p)
		newC, newPresent, newSkip := readDisk(repoRoot, p)
		if oldSkip || newSkip {
			// Intentionally skipped on a side (symlink, too large, binary,
			// unreadable, or escaping the repo). Omit the file entirely rather
			// than render a misleading full add/delete — or leak a symlink target.
			continue
		}
		if !oldPresent && !newPresent {
			continue // absent on both — nothing to show
		}
		if oldC == newC {
			continue // no textual change
		}
		files = append(files, FileDiff{Path: relToDir(p, prefix), Rows: unifiedRows(oldC, newC)})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// headResolver returns a lookup that yields the content of a repo-relative path
// at HEAD, with (content, present, skipped) semantics matching readDisk:
// present=false → absent at HEAD (an added file); present=true & skipped=true →
// exists but too large/binary/unreadable (omit it). A repo with no HEAD yields
// all-absent.
func (s *Service) headResolver(repo *gogit.Repository) func(path string) (string, bool, bool) {
	absent := func(string) (string, bool, bool) { return "", false, false }
	ref, err := repo.Head()
	if err != nil {
		return absent
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return absent
	}
	tree, err := commit.Tree()
	if err != nil {
		return absent
	}
	return func(path string) (string, bool, bool) {
		f, err := tree.File(path)
		if err != nil {
			return "", false, false // not present at HEAD
		}
		if f.Size > maxFileBytes {
			return "", true, true
		}
		c, err := f.Contents()
		if err != nil || isBinaryStr(c) {
			return "", true, true
		}
		return c, true, false
	}
}

// evalSymlinks resolves a path to its canonical form, returning the input
// unchanged if it cannot be resolved (best-effort; never fails the caller).
func evalSymlinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// withinPrefix reports whether repo-relative path p is inside the served dir
// identified by prefix ("" means the served dir is the repo root).
func withinPrefix(p, prefix string) bool {
	if prefix == "" {
		return true
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// relToDir turns a repo-relative path into a served-dir-relative path.
func relToDir(p, prefix string) string {
	if prefix == "" {
		return p
	}
	return strings.TrimPrefix(p, prefix+"/")
}

// readDisk reads the on-disk (working tree) content of a repo-relative path.
// Returns (content, present, skipped):
//   - present=false               → the file is absent on disk (a deletion).
//   - present=true, skipped=true  → the file exists but must not be diffed
//     (a symlink, a non-regular file, too large, binary, unreadable, or a path
//     that resolves OUTSIDE the repo). The caller omits it entirely.
//   - present=true, skipped=false → content is the file's text.
//
// It uses os.Lstat (NOT os.Stat) and refuses symlinks: a symlinked .md such as
// leak.md -> /etc/passwd would otherwise be read through and leaked via the diff
// endpoint. As defense in depth it also rejects any path whose resolved location
// escapes the repo root (guards a symlinked *intermediate* directory).
func readDisk(repoRoot, p string) (content string, present, skipped bool) {
	full := filepath.Join(repoRoot, filepath.FromSlash(p))
	fi, err := os.Lstat(full)
	if err != nil {
		return "", false, false // absent → a deletion
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return "", true, true // never follow symlinks / non-regular files
	}
	if rp := evalSymlinks(full); !withinDir(evalSymlinks(repoRoot), rp) {
		return "", true, true // resolves outside the repo (symlinked ancestor)
	}
	if fi.Size() > maxFileBytes {
		return "", true, true
	}
	b, err := os.ReadFile(full)
	if err != nil || isBinary(b) {
		return "", true, true
	}
	return string(b), true, false
}

// withinDir reports whether path is root itself or nested under it.
func withinDir(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

// isBinary flags content that should not be rendered as a text diff: a NUL byte
// is the classic heuristic and never appears in Markdown.
func isBinary(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// isBinary for strings (HEAD blob contents come back as a string).
func isBinaryStr(s string) bool { return strings.IndexByte(s, 0) >= 0 }

// unifiedRows builds a collapsed, line-based unified diff. It is a deliberate
// port of unifiedDiff() in web/src/suggestion/diff.ts: only changed lines plus
// ctxLines of context are kept, and long unchanged runs collapse to a single
// "… N unchanged lines" gap row, so a Go-built diff renders identically to the
// frontend-built one.
func unifiedRows(before, after string) []Row {
	dmp := diffmatchpatch.New()
	a, b, lineArray := dmp.DiffLinesToChars(before, after)
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	var lines []Row
	for _, d := range diffs {
		parts := strings.Split(d.Text, "\n")
		if n := len(parts); n > 0 && parts[n-1] == "" {
			parts = parts[:n-1]
		}
		kind := "eq"
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			kind = "ins"
		case diffmatchpatch.DiffDelete:
			kind = "del"
		}
		for _, p := range parts {
			lines = append(lines, Row{Op: kind, Text: p})
		}
	}

	var rows []Row
	i := 0
	for i < len(lines) {
		if lines[i].Op != "eq" {
			rows = append(rows, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && lines[j].Op == "eq" {
			j++
		}
		runLen := j - i
		showStart := ctxLines // trailing context of the change above
		if i == 0 {
			showStart = 0
		}
		showEnd := ctxLines // leading context of the change below
		if j == len(lines) {
			showEnd = 0
		}
		if showStart+showEnd >= runLen {
			rows = append(rows, lines[i:j]...)
		} else {
			rows = append(rows, lines[i:i+showStart]...)
			rows = append(rows, Row{Op: "gap", Text: fmt.Sprintf("… %d unchanged lines", runLen-showStart-showEnd)})
			rows = append(rows, lines[j-showEnd:j]...)
		}
		i = j
	}
	return rows
}

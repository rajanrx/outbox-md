package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a git work tree at dir with one committed file, using the
// git binary ONLY to build the fixture — the code under test uses go-git.
func initRepo(t *testing.T, dir, file, content string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available to build fixture")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
}

func TestOpenDetectsGit(t *testing.T) {
	dir := t.TempDir()
	if Open(dir).HasGit() {
		t.Fatal("plain dir reported as git")
	}
	if (*Service)(nil).HasGit() {
		t.Fatal("nil service reported as git")
	}

	repo := t.TempDir()
	initRepo(t, repo, "spec.md", "one\ntwo\nthree\n")
	if !Open(repo).HasGit() {
		t.Fatal("git work tree not detected")
	}
}

func TestDiffReportsModifiedFile(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "spec.md", "one\ntwo\nthree\n")
	// Modify the working tree without committing.
	if err := os.WriteFile(filepath.Join(repo, "spec.md"), []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Open(repo).Diff(context.Background())
	if !res.Enabled {
		t.Fatal("expected enabled")
	}
	if len(res.Files) != 1 || res.Files[0].Path != "spec.md" {
		t.Fatalf("files = %+v", res.Files)
	}
	var ins, del bool
	for _, r := range res.Files[0].Rows {
		if r.Op == "ins" && r.Text == "TWO" {
			ins = true
		}
		if r.Op == "del" && r.Text == "two" {
			del = true
		}
	}
	if !ins || !del {
		t.Fatalf("expected ins+del rows, got %+v", res.Files[0].Rows)
	}
}

func TestDiffNonGitDisabled(t *testing.T) {
	res := Open(t.TempDir()).Diff(context.Background())
	if res.Enabled {
		t.Fatal("non-git dir should be disabled")
	}
	if res.Files == nil {
		t.Fatal("Files must be a non-nil (empty) slice")
	}
}

func TestDiffNilServiceSafe(t *testing.T) {
	res := (*Service)(nil).Diff(context.Background())
	if res.Enabled || res.Files == nil {
		t.Fatalf("nil service diff = %+v", res)
	}
}

func TestDiffAddedAndDeleted(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "kept.md", "hello\n")
	// Add a brand new file (untracked) and delete the committed one.
	if err := os.WriteFile(filepath.Join(repo, "new.md"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "kept.md")); err != nil {
		t.Fatal(err)
	}

	res := Open(repo).Diff(context.Background())
	byPath := map[string][]Row{}
	for _, f := range res.Files {
		byPath[f.Path] = f.Rows
	}
	if rows, ok := byPath["new.md"]; !ok || !hasOp(rows, "ins") {
		t.Fatalf("new.md should be all-ins: %+v", byPath)
	}
	if rows, ok := byPath["kept.md"]; !ok || !hasOp(rows, "del") {
		t.Fatalf("kept.md should be all-del: %+v", byPath)
	}
}

func TestDiffOnlyServedSubdir(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "specs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, repo, filepath.Join("specs", "in.md"), "a\n")
	// A change outside the served subdir must be excluded.
	if err := os.WriteFile(filepath.Join(repo, "outside.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "in.md"), []byte("A\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Open(sub).Diff(context.Background())
	if len(res.Files) != 1 || res.Files[0].Path != "in.md" {
		t.Fatalf("served-subdir diff = %+v (path must be dir-relative, outside.md excluded)", res.Files)
	}
}

func hasOp(rows []Row, op string) bool {
	for _, r := range rows {
		if r.Op == op {
			return true
		}
	}
	return false
}

package registry

import (
	"os"
	"path/filepath"
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

func TestAddListRoundTrip(t *testing.T) {
	file := regFile(t)
	dir := t.TempDir()

	p, err := Add(file, dir)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if p.Name != filepath.Base(dir) {
		t.Fatalf("name = %q, want basename %q", p.Name, filepath.Base(dir))
	}
	if !filepath.IsAbs(p.Path) {
		t.Fatalf("path %q is not absolute", p.Path)
	}

	list, err := List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != p.Path {
		t.Fatalf("list = %v, want single entry %v", list, p)
	}
}

func TestAddIsIdempotentByPath(t *testing.T) {
	file := regFile(t)
	dir := t.TempDir()
	if _, err := Add(file, dir); err != nil {
		t.Fatal(err)
	}
	// Adding the same folder again must not duplicate it.
	if _, err := Add(file, dir); err != nil {
		t.Fatal(err)
	}
	list, _ := List(file)
	if len(list) != 1 {
		t.Fatalf("dedupe failed: %d entries, want 1", len(list))
	}
}

func TestAddMissingPathErrors(t *testing.T) {
	file := regFile(t)
	if _, err := Add(file, filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected error adding a missing directory")
	}
}

func TestAddFileNotDirErrors(t *testing.T) {
	file := regFile(t)
	f := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(file, f); err == nil {
		t.Fatal("expected error adding a file (not a directory)")
	}
}

// TestAddDisambiguatesNameCollision verifies two different folders that share a
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
	pa, err := Add(file, a)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Add(file, b)
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

func TestRemoveByNameAndPath(t *testing.T) {
	file := regFile(t)
	d1, d2 := t.TempDir(), t.TempDir()
	p1, _ := Add(file, d1)
	p2, _ := Add(file, d2)

	// Remove by name.
	removed, err := Remove(file, p1.Name)
	if err != nil || !removed {
		t.Fatalf("remove by name: removed=%v err=%v", removed, err)
	}
	// Remove by path.
	removed, err = Remove(file, p2.Path)
	if err != nil || !removed {
		t.Fatalf("remove by path: removed=%v err=%v", removed, err)
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

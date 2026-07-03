package rag

import (
	"context"
	"testing"
)

func TestCmdEmbedderSuccessAndDimsDiscovery(t *testing.T) {
	e := NewCmdEmbedder(fakeEmbedCmd(t))
	if e.Dims() != 0 {
		t.Fatalf("Dims before embed = %d, want 0 (undiscovered)", e.Dims())
	}
	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if len(vecs[0]) != 4 {
		t.Fatalf("vector dims = %d, want 4", len(vecs[0]))
	}
	if e.Dims() != 4 {
		t.Fatalf("Dims after embed = %d, want 4 (discovered)", e.Dims())
	}
}

func TestCmdEmbedderIDChangesWithCommand(t *testing.T) {
	a := NewCmdEmbedder("embed-tool-a --json")
	b := NewCmdEmbedder("embed-tool-b --json")
	if a.ID() == b.ID() {
		t.Fatalf("different commands share ID %q — re-index would not trigger", a.ID())
	}
	// Same command → stable ID.
	if a.ID() != NewCmdEmbedder("embed-tool-a --json").ID() {
		t.Fatal("same command produced different IDs")
	}
	// ID has the documented cmd: prefix.
	if a.ID()[:4] != "cmd:" {
		t.Fatalf("ID %q missing cmd: prefix", a.ID())
	}
}

func TestCmdEmbedderFailingCommandSurfacesError(t *testing.T) {
	e := NewCmdEmbedder("this-binary-definitely-does-not-exist-xyz123")
	vecs, err := e.Embed(context.Background(), []string{"anything"})
	if err == nil {
		t.Fatal("expected error from a non-existent command, got nil")
	}
	if vecs != nil {
		t.Fatal("expected no vectors on failure")
	}
}

func TestCmdEmbedderEmptyInput(t *testing.T) {
	e := NewCmdEmbedder(fakeEmbedCmd(t))
	vecs, err := e.Embed(context.Background(), nil)
	if err != nil || vecs != nil {
		t.Fatalf("empty input: vecs=%v err=%v, want nil,nil", vecs, err)
	}
}

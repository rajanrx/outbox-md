package store

import (
	"errors"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func TestAddVersionTxCAS(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, v1, _ := s.CreateDocument("spec.md", "a", "human")

	// Advance v1 -> v2 (CAS against the real current version succeeds).
	v2, err := s.AddVersionTx(doc.ID, v1.ID, "b", "agent", nil)
	if err != nil {
		t.Fatal(err)
	}

	// A second CAS against the now-stale v1 must conflict, not advance.
	if _, err := s.AddVersionTx(doc.ID, v1.ID, "c", "agent", nil); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}
	cur, _ := s.GetDocument(doc.ID)
	if cur.CurrentVersionID != v2.ID {
		t.Fatal("conflicting CAS must not advance the current version")
	}

	// CAS against the real current version (v2) succeeds.
	if _, err := s.AddVersionTx(doc.ID, v2.ID, "c", "agent", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndVersion(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	doc, v1, err := s.CreateDocument("spec.md", "hello", "human")
	if err != nil {
		t.Fatal(err)
	}
	if v1.Ordinal != 1 || v1.Content != "hello" {
		t.Fatalf("v1 = %+v", v1)
	}
	if doc.CurrentVersionID != v1.ID {
		t.Fatal("current version not set to v1")
	}

	v2, err := s.AddVersion(doc.ID, "hello world", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if v2.Ordinal != 2 {
		t.Fatalf("v2 ordinal = %d, want 2", v2.Ordinal)
	}
	got, _ := s.GetDocument(doc.ID)
	if got.CurrentVersionID != v2.ID {
		t.Fatal("current version not advanced to v2")
	}
}

func TestNewDocumentDefaultsToDraft(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	doc, _, err := s.CreateDocument("a.md", "hello", "human")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DocDraft {
		t.Errorf("status = %q, want draft", got.Status)
	}
	if got.ApprovedVersionID != "" {
		t.Errorf("approvedVersionId = %q, want empty", got.ApprovedVersionID)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	// migrate already ran inside Open; running it again must be a no-op.
	if err := migrate(s.DB); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
}

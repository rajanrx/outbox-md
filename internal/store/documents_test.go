package store

import "testing"

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

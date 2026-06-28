package domain

import "testing"

func TestNewIDUniqueAndNonEmpty(t *testing.T) {
	a, b := NewID(), NewID()
	if a == "" || b == "" {
		t.Fatal("NewID returned empty")
	}
	if a == b {
		t.Fatalf("NewID not unique: %q == %q", a, b)
	}
}

package store

import "testing"

func TestOpenCreatesTables(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	row := s.DB.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN
		 ('documents','versions','comments','suggestions','thread_messages')`)
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("tables = %d, want 5", n)
	}
}

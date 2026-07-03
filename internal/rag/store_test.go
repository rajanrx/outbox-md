package rag

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// openDB gives each test an isolated in-memory SQLite. MaxOpenConns(1) keeps the
// ":memory:" database consistent across the database/sql pool (same rule as the
// governance store).
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(openDB(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestFTS5AvailableUnderModernc is the load-bearing probe: the whole lexical layer
// assumes modernc.org/sqlite ships FTS5. If this fails the store falls back to
// LIKE — this test asserts the CREATE VIRTUAL TABLE ... USING fts5 succeeded so we
// can trust the BM25 path.
func TestFTS5AvailableUnderModernc(t *testing.T) {
	s := newStore(t)
	if !s.FTSAvailable() {
		t.Fatal("FTS5 not available under modernc.org/sqlite — store fell back to LIKE")
	}
}

func rowFor(id, project, doc, heading, text string, ord int, hash, embID string, dims int, vec []float32) ChunkRow {
	return ChunkRow{
		ID: id, Project: project, DocPath: doc, Heading: heading, Ord: ord,
		Text: text, ContentHash: hash, EmbedderID: embID, Dims: dims, Embedding: vec,
	}
}

func countChunks(t *testing.T, s *Store, project, doc string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM chunks WHERE project=? AND doc_path=?`, project, doc).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countFTS(t *testing.T, s *Store, project, doc string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM chunks_fts f JOIN chunks c ON c.id=f.chunk_id
		 WHERE c.project=? AND c.doc_path=?`, project, doc).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUpsertReplacesPriorChunksAndSyncsFTS(t *testing.T) {
	s := newStore(t)
	// First index: 2 chunks.
	first := []ChunkRow{
		rowFor("", "p", "a.md", "H1", "the quick brown fox", 0, "hash1", "", 0, nil),
		rowFor("", "p", "a.md", "H2", "jumps over the lazy dog", 1, "hash1", "", 0, nil),
	}
	if err := s.UpsertDoc("p", "a.md", first); err != nil {
		t.Fatal(err)
	}
	if got := countChunks(t, s, "p", "a.md"); got != 2 {
		t.Fatalf("after first upsert chunks=%d, want 2", got)
	}
	if got := countFTS(t, s, "p", "a.md"); got != 2 {
		t.Fatalf("after first upsert fts rows=%d, want 2 (in sync)", got)
	}

	// Re-index the same doc with 1 chunk: prior chunks AND their FTS rows must go.
	second := []ChunkRow{rowFor("", "p", "a.md", "H", "entirely new content", 0, "hash2", "", 0, nil)}
	if err := s.UpsertDoc("p", "a.md", second); err != nil {
		t.Fatal(err)
	}
	if got := countChunks(t, s, "p", "a.md"); got != 1 {
		t.Fatalf("after re-upsert chunks=%d, want 1 (replaced)", got)
	}
	if got := countFTS(t, s, "p", "a.md"); got != 1 {
		t.Fatalf("after re-upsert fts rows=%d, want 1 (stale FTS purged)", got)
	}
	// Old text is gone from FTS: a search for "fox" returns nothing now.
	hits, err := s.LexicalSearch("p", "fox", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("stale term 'fox' still matched %d rows after replace", len(hits))
	}
}

func TestEmbeddingBlobRoundTrips(t *testing.T) {
	s := newStore(t)
	vec := []float32{0.5, -1.25, 3.0, 0.0, 2.5}
	err := s.UpsertDoc("p", "v.md", []ChunkRow{
		rowFor("", "p", "v.md", "H", "vectorised chunk", 0, "h", "cmd:abc", 5, vec),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.VectorChunks("p", "cmd:abc", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("VectorChunks=%d, want 1", len(got))
	}
	if len(got[0].Embedding) != len(vec) {
		t.Fatalf("dims round-trip: got %d, want %d", len(got[0].Embedding), len(vec))
	}
	for i := range vec {
		if got[0].Embedding[i] != vec[i] {
			t.Fatalf("embedding[%d]=%v, want %v", i, got[0].Embedding[i], vec[i])
		}
	}
}

func TestVectorChunksKeyedByEmbedderAndDims(t *testing.T) {
	s := newStore(t)
	vec := []float32{1, 0, 0}
	if err := s.UpsertDoc("p", "k.md", []ChunkRow{
		rowFor("", "p", "k.md", "H", "chunk text", 0, "h", "cmd:aaaa", 3, vec),
	}); err != nil {
		t.Fatal(err)
	}
	// Same embedder+dims → visible.
	if got, _ := s.VectorChunks("p", "cmd:aaaa", 3); len(got) != 1 {
		t.Fatalf("same-embedder vector chunks=%d, want 1", len(got))
	}
	// Different embedder id → stale, not returned.
	if got, _ := s.VectorChunks("p", "cmd:bbbb", 3); len(got) != 0 {
		t.Fatalf("different-embedder vector chunks=%d, want 0 (stale)", len(got))
	}
	// Different dims → stale, not returned.
	if got, _ := s.VectorChunks("p", "cmd:aaaa", 768); len(got) != 0 {
		t.Fatalf("different-dims vector chunks=%d, want 0 (stale)", len(got))
	}
	// The stale chunk is still lexically retrievable (FTS carries it).
	if hits, _ := s.LexicalSearch("p", "chunk", 5); len(hits) != 1 {
		t.Fatalf("stale-vector chunk lexical hits=%d, want 1", len(hits))
	}
}

func TestLexicalSearchScopedByProject(t *testing.T) {
	s := newStore(t)
	must := func(p, doc, text string) {
		if err := s.UpsertDoc(p, doc, []ChunkRow{rowFor("", p, doc, "H", text, 0, "h", "", 0, nil)}); err != nil {
			t.Fatal(err)
		}
	}
	must("p1", "a.md", "shared keyword alpha")
	must("p2", "b.md", "shared keyword beta")
	hits, err := s.LexicalSearch("p1", "shared", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].DocPath != "a.md" {
		t.Fatalf("project-scoped search leaked: %+v", hits)
	}
}

func TestDeleteDocDropsChunksAndFTS(t *testing.T) {
	s := newStore(t)
	if err := s.UpsertDoc("p", "d.md", []ChunkRow{rowFor("", "p", "d.md", "H", "to be deleted", 0, "h", "", 0, nil)}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDoc("p", "d.md"); err != nil {
		t.Fatal(err)
	}
	if got := countChunks(t, s, "p", "d.md"); got != 0 {
		t.Fatalf("chunks after delete=%d, want 0", got)
	}
	if got := countFTS(t, s, "p", "d.md"); got != 0 {
		t.Fatalf("fts rows after delete=%d, want 0", got)
	}
}

func TestDocContentHash(t *testing.T) {
	s := newStore(t)
	if _, ok, err := s.DocContentHash("p", "missing.md"); err != nil || ok {
		t.Fatalf("unknown doc hash: ok=%v err=%v, want ok=false", ok, err)
	}
	if err := s.UpsertDoc("p", "h.md", []ChunkRow{
		rowFor("", "p", "h.md", "H", "x", 0, "deadbeef", "", 0, nil),
		rowFor("", "p", "h.md", "H", "y", 1, "deadbeef", "", 0, nil),
	}); err != nil {
		t.Fatal(err)
	}
	hash, ok, err := s.DocContentHash("p", "h.md")
	if err != nil || !ok || hash != "deadbeef" {
		t.Fatalf("DocContentHash = %q ok=%v err=%v, want deadbeef", hash, ok, err)
	}
}

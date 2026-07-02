package rag

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// ChunkRow is a persisted chunk: its text and citation metadata, a per-doc
// content hash (for §5.1 reconciliation), and — when an embedder produced one —
// its vector plus the embedder identity/dims the index is keyed by (§5.2). A row
// with EmbedderID == "" (or Embedding == nil) is text/FTS5-only: retrievable by
// keyword, invisible to vector search until embedded.
type ChunkRow struct {
	ID          string
	Project     string
	DocPath     string
	Heading     string
	Ord         int
	Text        string
	ContentHash string
	EmbedderID  string
	Dims        int
	Embedding   []float32
}

// Store is the RAG persistence layer over a *sql.DB (injected, so the server owns
// the connection). It manages the chunks table and a parallel FTS5 virtual table,
// kept in sync on every write. It is separate from internal/store so 3B/3C can
// wire it in without touching the governance schema.
type Store struct {
	db  *sql.DB
	fts bool // FTS5 available under this driver; false ⇒ LIKE lexical fallback
}

// NewStore initialises the RAG schema (idempotent) and probes FTS5 availability.
// modernc.org/sqlite ships FTS5, so the virtual table is expected to create; if
// it does not (an unexpected build), the store falls back to a LIKE token scan for
// the lexical layer and reports it via FTSAvailable so the caller can log it.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if _, err := db.Exec(chunksSchema); err != nil {
		return nil, fmt.Errorf("rag: create chunks table: %w", err)
	}
	if _, err := db.Exec(chunksFTSSchema); err != nil {
		if isNoFTS5(err) {
			s.fts = false
			return s, nil
		}
		return nil, fmt.Errorf("rag: create fts5 table: %w", err)
	}
	s.fts = true
	return s, nil
}

// FTSAvailable reports whether the FTS5 lexical layer is active (true) or the
// store fell back to a LIKE scan (false).
func (s *Store) FTSAvailable() bool { return s.fts }

const chunksSchema = `
CREATE TABLE IF NOT EXISTS chunks (
  id TEXT PRIMARY KEY,
  project TEXT NOT NULL DEFAULT '',
  doc_path TEXT NOT NULL,
  heading TEXT NOT NULL DEFAULT '',
  ord INTEGER NOT NULL DEFAULT 0,
  text TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  embedder_id TEXT NOT NULL DEFAULT '',
  dims INTEGER NOT NULL DEFAULT 0,
  embedding BLOB
);
CREATE INDEX IF NOT EXISTS idx_chunks_project_doc ON chunks(project, doc_path);
`

// chunksFTS is a standalone FTS5 table (not external-content: chunk ids are TEXT,
// so we keep the lexical rows in lock-step manually). project/chunk_id are
// UNINDEXED so we can filter a MATCH by project and map hits back to chunks.
const chunksFTSSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
  chunk_id UNINDEXED,
  project UNINDEXED,
  text,
  tokenize = 'porter unicode61'
);
`

func isNoFTS5(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such module: fts5")
}

// UpsertDoc replaces ALL chunks for (project, docPath) with the given rows in one
// transaction: prior chunks (and their FTS rows) are deleted, then the new rows
// inserted and mirrored into FTS5. This is the atomic "re-index this doc" write —
// a re-chunk never leaves a doc half-indexed. Rows should already carry Project +
// DocPath matching the arguments; they are set defensively.
func (s *Store) UpsertDoc(project, docPath string, rows []ChunkRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteDocTx(tx, s.fts, project, docPath); err != nil {
		return err
	}
	for i := range rows {
		r := rows[i]
		r.Project, r.DocPath = project, docPath
		if r.ID == "" {
			r.ID = domain.NewID()
		}
		if err := insertChunkTx(tx, s.fts, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteDoc drops a doc's chunks (and FTS rows). A rename is delete + upsert.
func (s *Store) DeleteDoc(project, docPath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteDocTx(tx, s.fts, project, docPath); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteDocTx(tx *sql.Tx, fts bool, project, docPath string) error {
	if fts {
		// Delete FTS rows by the chunk-id set being removed (delete-by-UNINDEXED-col
		// is not reliable on a standalone FTS5 table, so we scope by chunk_id).
		if _, err := tx.Exec(
			`DELETE FROM chunks_fts WHERE chunk_id IN (
			   SELECT id FROM chunks WHERE project=? AND doc_path=?)`,
			project, docPath); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`DELETE FROM chunks WHERE project=? AND doc_path=?`, project, docPath)
	return err
}

func insertChunkTx(tx *sql.Tx, fts bool, r ChunkRow) error {
	var blob any
	if len(r.Embedding) > 0 {
		blob = float32sToBlob(r.Embedding)
	}
	if _, err := tx.Exec(
		`INSERT INTO chunks(id, project, doc_path, heading, ord, text, content_hash, embedder_id, dims, embedding)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Project, r.DocPath, r.Heading, r.Ord, r.Text, r.ContentHash, r.EmbedderID, r.Dims, blob); err != nil {
		return err
	}
	if fts {
		if _, err := tx.Exec(
			`INSERT INTO chunks_fts(chunk_id, project, text) VALUES(?,?,?)`,
			r.ID, r.Project, r.Text); err != nil {
			return err
		}
	}
	return nil
}

// DocContentHash returns the stored content hash for a (project, docPath). All of
// a doc's chunks share the doc's hash, so any one row answers it. ok is false when
// the doc has no chunks (unknown / never indexed) — 3B reconciliation compares
// this against the on-disk file hash to decide re-embed.
func (s *Store) DocContentHash(project, docPath string) (hash string, ok bool, err error) {
	err = s.db.QueryRow(
		`SELECT content_hash FROM chunks WHERE project=? AND doc_path=? LIMIT 1`,
		project, docPath).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return hash, err == nil, err
}

// VectorChunk is a chunk with its decoded vector, for cosine scoring.
type VectorChunk struct {
	ID        string
	DocPath   string
	Heading   string
	Text      string
	Embedding []float32
}

// VectorChunks returns a project's chunks whose embedding is keyed by the given
// embedder id AND dims — i.e. comparable with a query embedded by the SAME
// embedder. Chunks embedded by a different embedder (or not embedded) are stale
// for this search and skipped; FTS5 still carries them. This is the §5.2
// ID()+Dims() index-key invariant enforced at read time.
func (s *Store) VectorChunks(project, embedderID string, dims int) ([]VectorChunk, error) {
	rows, err := s.db.Query(
		`SELECT id, doc_path, heading, text, embedding
		   FROM chunks
		  WHERE project=? AND embedder_id=? AND dims=? AND embedding IS NOT NULL`,
		project, embedderID, dims)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VectorChunk
	for rows.Next() {
		var vc VectorChunk
		var blob []byte
		if err := rows.Scan(&vc.ID, &vc.DocPath, &vc.Heading, &vc.Text, &blob); err != nil {
			return nil, err
		}
		vc.Embedding = blobToFloat32s(blob)
		out = append(out, vc)
	}
	return out, rows.Err()
}

// LexicalHit is a keyword-search result: a chunk plus its lexical score (higher =
// better; already sign-flipped from BM25 for the FTS path).
type LexicalHit struct {
	ID      string
	DocPath string
	Heading string
	Text    string
	Score   float64
}

// LexicalSearch runs the always-on keyword layer scoped to a project, returning up
// to limit hits ranked best-first. It uses FTS5/BM25 when available, falling back
// to a LIKE token scan otherwise. It is the retrieval floor: it works with no
// embedder at all (design §5.2).
func (s *Store) LexicalSearch(project, query string, limit int) ([]LexicalHit, error) {
	if limit <= 0 {
		limit = 10
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if s.fts {
		return s.ftsSearch(project, query, limit)
	}
	return s.likeSearch(project, query, limit)
}

func (s *Store) ftsSearch(project, query string, limit int) ([]LexicalHit, error) {
	// bm25() is more-negative-is-better; negate so higher = more relevant.
	rows, err := s.db.Query(
		`SELECT f.chunk_id, c.doc_path, c.heading, c.text, -bm25(chunks_fts) AS score
		   FROM chunks_fts f JOIN chunks c ON c.id = f.chunk_id
		  WHERE f.project=? AND chunks_fts MATCH ?
		  ORDER BY score DESC LIMIT ?`,
		project, ftsMatchQuery(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LexicalHit
	for rows.Next() {
		var h LexicalHit
		if err := rows.Scan(&h.ID, &h.DocPath, &h.Heading, &h.Text, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsMatchQuery turns a free-text query into a safe FTS5 MATCH expression: each
// alphanumeric token is OR'd, so a multi-word query returns any-term matches
// (ranked by bm25) rather than requiring the exact phrase. Non-word characters
// that would be FTS5 syntax are dropped.
func ftsMatchQuery(query string) string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !(r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9'))
	})
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+f+`"`)
	}
	if len(quoted) == 0 {
		return `""`
	}
	return strings.Join(quoted, " OR ")
}

// likeSearch is the FTS5-absent fallback: a per-token LIKE scan scoring by the
// number of distinct query tokens a chunk contains. Coarse but keeps retrieval
// working without FTS5 (the spec's degrade-don't-break floor).
func (s *Store) likeSearch(project, query string, limit int) ([]LexicalHit, error) {
	tokens := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9'))
	})
	if len(tokens) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, doc_path, heading, text FROM chunks WHERE project=?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LexicalHit
	for rows.Next() {
		var h LexicalHit
		if err := rows.Scan(&h.ID, &h.DocPath, &h.Heading, &h.Text); err != nil {
			return nil, err
		}
		lower := strings.ToLower(h.Text)
		score := 0.0
		for _, tok := range tokens {
			if strings.Contains(lower, tok) {
				score++
			}
		}
		if score > 0 {
			h.Score = score
			out = append(out, h)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortHitsDesc(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func sortHitsDesc(hits []LexicalHit) {
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].Score > hits[j-1].Score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
}

// float32sToBlob encodes a vector as little-endian float32 bytes (4 bytes each).
func float32sToBlob(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// blobToFloat32s decodes a little-endian float32 BLOB back into a vector. A blob
// whose length is not a multiple of 4 yields nil (corrupt row → treated as no
// vector).
func blobToFloat32s(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

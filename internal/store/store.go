package store

import (
	"database/sql"
	_ "embed"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct{ DB *sql.DB }

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Embedded single-process SQLite: serialize access through one connection.
	// This avoids SQLITE_BUSY under concurrent writers and keeps an in-memory
	// (":memory:") database consistent across the database/sql pool.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// migrate adds columns introduced after a database was first created. SQLite
// has no "ADD COLUMN IF NOT EXISTS", so a duplicate-column error is expected
// and ignored on databases that already have the column (CREATE TABLE above
// covers fresh databases).
func migrate(db *sql.DB) error {
	for _, stmt := range []string{
		`ALTER TABLE documents ADD COLUMN status TEXT NOT NULL DEFAULT 'draft'`,
		`ALTER TABLE documents ADD COLUMN approved_version_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE comments ADD COLUMN post_approval INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE comments ADD COLUMN processing_until TEXT`,
		// Stale-claim recovery: the instant a comment last entered 'claimed'
		// status. A legacy claimed row has NULL here → treated as a stale claim
		// (re-surfaced), which is exactly the recovery we want for comments
		// stranded before this column existed.
		`ALTER TABLE comments ADD COLUMN claimed_at TEXT`,
		// Multi-project: docs are keyed by (project, path). A pre-existing database
		// gets the column here; fresh databases already have it from schema.sql.
		`ALTER TABLE documents ADD COLUMN project TEXT NOT NULL DEFAULT ''`,
		// AI Council: the chair's confidence (0..100) in a synthesis. Legacy rows
		// default to 0; fresh databases already have it from schema.sql.
		`ALTER TABLE syntheses ADD COLUMN confidence INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	// Enforce the (project, path) key on legacy databases too (fresh ones already
	// have it from schema.sql). Idempotent — a no-op once the index exists. Legacy
	// databases still carry the original standalone UNIQUE(path) from their first
	// CREATE TABLE; that is harmless because legacy data is all project '' (the
	// single-folder mode), so path stays unique within that one project.
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_project_path ON documents(project, path)`); err != nil {
		return err
	}
	return nil
}

func (s *Store) Close() error { return s.DB.Close() }

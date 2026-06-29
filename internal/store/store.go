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
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.DB.Close() }

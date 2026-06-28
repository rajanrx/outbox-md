package store

import (
	"database/sql"
	_ "embed"

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
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

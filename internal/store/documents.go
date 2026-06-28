package store

import (
	"database/sql"
	"errors"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func (s *Store) CreateDocument(path, content, createdBy string) (domain.Document, domain.Version, error) {
	docID := domain.NewID()
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO documents(id, path, current_version_id) VALUES(?,?,?)`,
		docID, path, verID); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, 1, content, createdBy); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Document{}, domain.Version{}, err
	}
	doc := domain.Document{ID: docID, Path: path, CurrentVersionID: verID}
	ver := domain.Version{ID: verID, DocID: docID, Ordinal: 1, Content: content, CreatedBy: createdBy}
	return doc, ver, nil
}

func (s *Store) GetDocument(id string) (domain.Document, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, path, current_version_id FROM documents WHERE id=?`, id).
		Scan(&d.ID, &d.Path, &cur)
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err
}

func (s *Store) GetDocumentByPath(path string) (domain.Document, bool, error) {
	var d domain.Document
	var cur *string
	err := s.DB.QueryRow(`SELECT id, path, current_version_id FROM documents WHERE path=?`, path).
		Scan(&d.ID, &d.Path, &cur)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, false, nil
	}
	if cur != nil {
		d.CurrentVersionID = *cur
	}
	return d, err == nil, err
}

func (s *Store) ListDocuments() ([]domain.Document, error) {
	rows, err := s.DB.Query(`SELECT id, path, COALESCE(current_version_id,'') FROM documents ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Document
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(&d.ID, &d.Path, &d.CurrentVersionID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetVersion(id string) (domain.Version, error) {
	var v domain.Version
	err := s.DB.QueryRow(
		`SELECT id, doc_id, ordinal, content, created_by FROM versions WHERE id=?`, id).
		Scan(&v.ID, &v.DocID, &v.Ordinal, &v.Content, &v.CreatedBy)
	return v, err
}

// ErrVersionConflict means the document's current version changed between the
// caller's staleness check and the transaction — another accept won the race.
var ErrVersionConflict = errors.New("version conflict: document advanced concurrently")

// AddVersionTx records a new version and advances the current pointer inside a
// single transaction, but only if the current pointer is still expectedCurrent
// (a compare-and-swap). This serializes concurrent accepts: two requests that
// both passed a staleness check against the same version cannot both commit —
// the second sees RowsAffected == 0 and gets ErrVersionConflict.
//
// The write callback (e.g. the file write) runs after the CAS succeeds and
// before commit, so the file is only written by the winning accept and the
// database is never left pointing at content the write rejected. Callers should
// still compensate the external side effect when write ran, since a commit
// failure after a successful write is the one window this cannot close alone.
func (s *Store) AddVersionTx(docID, expectedCurrent, content, createdBy string, write func(domain.Version) error) (domain.Version, error) {
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Version{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Compare-and-swap the current pointer; advance only if it is unchanged.
	res, err := tx.Exec(`UPDATE documents SET current_version_id=? WHERE id=? AND current_version_id=?`,
		verID, docID, expectedCurrent)
	if err != nil {
		return domain.Version{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.Version{}, err
	}
	if n != 1 {
		return domain.Version{}, ErrVersionConflict
	}

	var maxOrd int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0) FROM versions WHERE doc_id=?`, docID).
		Scan(&maxOrd); err != nil {
		return domain.Version{}, err
	}
	ord := maxOrd + 1
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, ord, content, createdBy); err != nil {
		return domain.Version{}, err
	}
	v := domain.Version{ID: verID, DocID: docID, Ordinal: ord, Content: content, CreatedBy: createdBy}
	if write != nil {
		if err := write(v); err != nil {
			return domain.Version{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.Version{}, err
	}
	committed = true
	return v, nil
}

func (s *Store) AddVersion(docID, content, createdBy string) (domain.Version, error) {
	verID := domain.NewID()
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Version{}, err
	}
	defer tx.Rollback()
	var maxOrd int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ordinal),0) FROM versions WHERE doc_id=?`, docID).
		Scan(&maxOrd); err != nil {
		return domain.Version{}, err
	}
	ord := maxOrd + 1
	if _, err := tx.Exec(`INSERT INTO versions(id, doc_id, ordinal, content, created_by) VALUES(?,?,?,?,?)`,
		verID, docID, ord, content, createdBy); err != nil {
		return domain.Version{}, err
	}
	if _, err := tx.Exec(`UPDATE documents SET current_version_id=? WHERE id=?`, verID, docID); err != nil {
		return domain.Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Version{}, err
	}
	return domain.Version{ID: verID, DocID: docID, Ordinal: ord, Content: content, CreatedBy: createdBy}, nil
}

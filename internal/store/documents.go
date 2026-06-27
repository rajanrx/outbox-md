package store

import "github.com/rajanrx/outbox-md/internal/domain"

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

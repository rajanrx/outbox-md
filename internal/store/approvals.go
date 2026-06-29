package store

import "github.com/rajanrx/outbox-md/internal/domain"

func (s *Store) CreateApproval(a domain.Approval) (domain.Approval, error) {
	if a.ID == "" {
		a.ID = domain.NewID()
	}
	_, err := s.DB.Exec(
		`INSERT INTO approvals(id, doc_id, version_id, approved_by, note) VALUES(?,?,?,?,?)`,
		a.ID, a.DocID, a.VersionID, a.ApprovedBy, a.Note)
	return a, err
}

func (s *Store) ListApprovals(docID string) ([]domain.Approval, error) {
	rows, err := s.DB.Query(
		`SELECT id, doc_id, version_id, approved_by, note, created_at
		 FROM approvals WHERE doc_id=? ORDER BY created_at`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Approval{}
	for rows.Next() {
		var a domain.Approval
		if err := rows.Scan(&a.ID, &a.DocID, &a.VersionID, &a.ApprovedBy, &a.Note, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

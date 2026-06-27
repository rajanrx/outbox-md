package store

import "github.com/rajanrx/outbox-md/internal/domain"

func scanComment(scan func(...any) error) (domain.Comment, error) {
	var c domain.Comment
	err := scan(&c.ID, &c.DocID, &c.AgainstVersionID, &c.Anchor.Start, &c.Anchor.End,
		&c.AuthorIdentity, &c.Owner, &c.Status, &c.ClaimToken)
	return c, err
}

const commentCols = `id, doc_id, against_version_id, anchor_start, anchor_end,
	author_identity, owner, status, claim_token`

func (s *Store) CreateComment(c domain.Comment) (domain.Comment, error) {
	if c.ID == "" {
		c.ID = domain.NewID()
	}
	_, err := s.DB.Exec(`INSERT INTO comments(`+commentCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		c.ID, c.DocID, c.AgainstVersionID, c.Anchor.Start, c.Anchor.End,
		c.AuthorIdentity, c.Owner, c.Status, c.ClaimToken)
	return c, err
}

func (s *Store) GetComment(id string) (domain.Comment, error) {
	return scanComment(s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id=?`, id).Scan)
}

func (s *Store) ListComments(docID string) ([]domain.Comment, error) {
	rows, err := s.DB.Query(`SELECT `+commentCols+` FROM comments WHERE doc_id=? ORDER BY created_at`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Comment
	for rows.Next() {
		c, err := scanComment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListOpenComments() ([]domain.Comment, error) {
	rows, err := s.DB.Query(`SELECT ` + commentCols + ` FROM comments WHERE status='open' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Comment
	for rows.Next() {
		c, err := scanComment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCommentStatus(id string, status domain.CommentStatus, claimToken string) error {
	_, err := s.DB.Exec(`UPDATE comments SET status=?, claim_token=? WHERE id=?`, status, claimToken, id)
	return err
}

func (s *Store) UpdateCommentAnchor(id string, a domain.Anchor, status domain.CommentStatus) error {
	_, err := s.DB.Exec(`UPDATE comments SET anchor_start=?, anchor_end=?, status=? WHERE id=?`,
		a.Start, a.End, status, id)
	return err
}

func (s *Store) RebaseComment(id, newVersionID string, a domain.Anchor, status domain.CommentStatus) error {
	_, err := s.DB.Exec(
		`UPDATE comments SET against_version_id=?, anchor_start=?, anchor_end=?, status=? WHERE id=?`,
		newVersionID, a.Start, a.End, status, id)
	return err
}

func (s *Store) AddThreadMessage(m domain.ThreadMessage) (domain.ThreadMessage, error) {
	if m.ID == "" {
		m.ID = domain.NewID()
	}
	_, err := s.DB.Exec(`INSERT INTO thread_messages(id, comment_id, author_identity, body) VALUES(?,?,?,?)`,
		m.ID, m.CommentID, m.AuthorIdentity, m.Body)
	return m, err
}

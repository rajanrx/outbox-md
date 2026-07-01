package store

import (
	"database/sql"
	"errors"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func (s *Store) CreateSuggestion(sg domain.Suggestion) (domain.Suggestion, error) {
	if sg.ID == "" {
		sg.ID = domain.NewID()
	}
	_, err := s.DB.Exec(
		`INSERT INTO suggestions(id, comment_id, against_version_id, proposed_content, state, created_by)
		 VALUES(?,?,?,?,?,?)`,
		sg.ID, sg.CommentID, sg.AgainstVersionID, sg.ProposedContent, sg.State, sg.CreatedBy)
	return sg, err
}

func (s *Store) GetSuggestionByComment(commentID string) (domain.Suggestion, bool, error) {
	var sg domain.Suggestion
	err := s.DB.QueryRow(
		`SELECT id, comment_id, against_version_id, proposed_content, state, created_by
		 FROM suggestions WHERE comment_id=? ORDER BY created_at DESC LIMIT 1`, commentID).
		Scan(&sg.ID, &sg.CommentID, &sg.AgainstVersionID, &sg.ProposedContent, &sg.State, &sg.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Suggestion{}, false, nil
	}
	return sg, err == nil, err
}

func (s *Store) UpdateSuggestionState(id string, state domain.SuggestionState) error {
	_, err := s.DB.Exec(`UPDATE suggestions SET state=? WHERE id=?`, state, id)
	return err
}

// RejectSuggestionIfProposed rejects a suggestion, but only if it is still
// proposed. This makes a losing/duplicate accept's requeue a no-op once a
// winning accept has marked the same suggestion accepted.
func (s *Store) RejectSuggestionIfProposed(id string) error {
	_, err := s.DB.Exec(`UPDATE suggestions SET state=? WHERE id=? AND state=?`,
		domain.SuggestionRejected, id, domain.SuggestionProposed)
	return err
}

// PendingSuggestion is the read model for the folder view: one doc that has a
// pending (proposed) suggestion, carrying the doc's current content and the
// proposed replacement so the UI can render the same current-vs-proposed diff
// it shows inline.
type PendingSuggestion struct {
	DocID     string `json:"docId"`
	Path      string `json:"path"`
	CommentID string `json:"commentId"`
	Current   string `json:"current"`
	Proposed  string `json:"proposed"`
}

// ListPendingSuggestions returns every comment across all docs whose latest
// suggestion is still proposed AND whose comment is still 'addressed', paired
// with the doc's current version content. Gating on comment status mirrors the
// inline "This change" surface (rendered only when comment.status ===
// "addressed"), so a comment the human has since resolved, replied to, or
// detached — which can leave a stale 'proposed' suggestion row behind — no
// longer lingers in the folder view. The "latest suggestion per comment"
// subquery mirrors GetSuggestionByComment.
func (s *Store) ListPendingSuggestions() ([]PendingSuggestion, error) {
	rows, err := s.DB.Query(`
		SELECT c.doc_id, d.path, s.comment_id, v.content, s.proposed_content
		FROM suggestions s
		JOIN comments c   ON c.id = s.comment_id
		JOIN documents d  ON d.id = c.doc_id
		JOIN versions v   ON v.id = d.current_version_id
		WHERE s.state = ?
		  AND c.status = ?
		  AND s.id = (
			SELECT id FROM suggestions s2
			WHERE s2.comment_id = s.comment_id
			ORDER BY created_at DESC LIMIT 1
		  )
		ORDER BY d.path`, domain.SuggestionProposed, domain.CommentAddressed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingSuggestion{}
	for rows.Next() {
		var p PendingSuggestion
		if err := rows.Scan(&p.DocID, &p.Path, &p.CommentID, &p.Current, &p.Proposed); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

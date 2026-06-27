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

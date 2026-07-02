package store

import (
	"encoding/json"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// AddDiscussionMessage appends one attributed message to a council's discussion
// transcript. Refs are stored as a JSON array in a single column; a nil/empty
// slice round-trips as "[]" so ListDiscussionByComment always returns a non-nil
// slice. It never touches the comment or the document — the transcript is purely
// additive.
func (s *Store) AddDiscussionMessage(m domain.DiscussionMessage) (domain.DiscussionMessage, error) {
	if m.ID == "" {
		m.ID = domain.NewID()
	}
	if m.Refs == nil {
		m.Refs = []domain.DiscussionRef{}
	}
	refs, err := json.Marshal(m.Refs)
	if err != nil {
		return domain.DiscussionMessage{}, err
	}
	_, err = s.DB.Exec(
		`INSERT INTO discussion_messages(id, candidate_set_id, round, agent_identity, body, refs)
		 VALUES(?,?,?,?,?,?)`,
		m.ID, m.CandidateSetID, m.Round, m.AgentIdentity, m.Body, string(refs))
	return m, err
}

// ListDiscussionByComment returns the comment's discussion transcript ordered by
// round, then insertion order (rowid) within a round — so a reader sees rounds in
// sequence and, within each round, messages in the order they were posted. It
// returns a non-nil (possibly empty) slice.
func (s *Store) ListDiscussionByComment(commentID string) ([]domain.DiscussionMessage, error) {
	rows, err := s.DB.Query(
		`SELECT d.id, d.candidate_set_id, d.round, d.agent_identity, d.body, d.refs
		 FROM discussion_messages d
		 JOIN candidate_sets cs ON d.candidate_set_id = cs.id
		 WHERE cs.comment_id=? ORDER BY d.round, d.rowid`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.DiscussionMessage{}
	for rows.Next() {
		var m domain.DiscussionMessage
		var refs string
		if err := rows.Scan(&m.ID, &m.CandidateSetID, &m.Round, &m.AgentIdentity, &m.Body, &refs); err != nil {
			return nil, err
		}
		if refs == "" {
			m.Refs = []domain.DiscussionRef{}
		} else if err := json.Unmarshal([]byte(refs), &m.Refs); err != nil {
			return nil, err
		}
		if m.Refs == nil {
			m.Refs = []domain.DiscussionRef{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

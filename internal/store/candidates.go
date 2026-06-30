package store

import (
	"database/sql"
	"errors"

	"github.com/rajanrx/outbox-md/internal/domain"
)

// GetOrCreateCandidateSet returns the comment's council, creating it lazily on
// the first review. One set per comment is enforced by the UNIQUE(comment_id)
// constraint; a concurrent create loses the INSERT and we re-read the winner.
func (s *Store) GetOrCreateCandidateSet(commentID string) (domain.CandidateSet, error) {
	set, ok, err := s.GetCandidateSetByComment(commentID)
	if err != nil {
		return domain.CandidateSet{}, err
	}
	if ok {
		return set, nil
	}
	set = domain.CandidateSet{
		ID: domain.NewID(), CommentID: commentID, State: domain.CandidateSetGathering,
	}
	_, err = s.DB.Exec(`INSERT INTO candidate_sets(id, comment_id, state, quorum) VALUES(?,?,?,?)`,
		set.ID, set.CommentID, set.State, set.Quorum)
	if err != nil {
		// Lost the create race: a concurrent reviewer inserted first. Re-read.
		if set2, ok2, err2 := s.GetCandidateSetByComment(commentID); err2 == nil && ok2 {
			return set2, nil
		}
		return domain.CandidateSet{}, err
	}
	return set, nil
}

func (s *Store) GetCandidateSetByComment(commentID string) (domain.CandidateSet, bool, error) {
	var set domain.CandidateSet
	err := s.DB.QueryRow(`SELECT id, comment_id, state, quorum FROM candidate_sets WHERE comment_id=?`, commentID).
		Scan(&set.ID, &set.CommentID, &set.State, &set.Quorum)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CandidateSet{}, false, nil
	}
	return set, err == nil, err
}

func (s *Store) SetCandidateSetState(id string, state domain.CandidateSetState) error {
	_, err := s.DB.Exec(`UPDATE candidate_sets SET state=? WHERE id=?`, state, id)
	return err
}

const candidateCols = `id, candidate_set_id, lens, verdict, rationale, content, agent_identity, chosen`

func scanCandidate(scan func(...any) error) (domain.Candidate, error) {
	var c domain.Candidate
	var chosen int
	err := scan(&c.ID, &c.CandidateSetID, &c.Lens, &c.Verdict, &c.Rationale, &c.Content, &c.AgentIdentity, &chosen)
	c.Chosen = chosen != 0
	return c, err
}

func (s *Store) AddCandidate(c domain.Candidate) (domain.Candidate, error) {
	if c.ID == "" {
		c.ID = domain.NewID()
	}
	chosen := 0
	if c.Chosen {
		chosen = 1
	}
	_, err := s.DB.Exec(`INSERT INTO candidates(`+candidateCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		c.ID, c.CandidateSetID, c.Lens, c.Verdict, c.Rationale, c.Content, c.AgentIdentity, chosen)
	return c, err
}

func (s *Store) GetCandidate(id string) (domain.Candidate, error) {
	return scanCandidate(s.DB.QueryRow(`SELECT `+candidateCols+` FROM candidates WHERE id=?`, id).Scan)
}

// ListCandidatesByComment returns the comment's candidates in submission order.
// The (created_at, rowid) sort keeps same-second inserts deterministic — the
// default datetime('now') has only one-second resolution.
func (s *Store) ListCandidatesByComment(commentID string) ([]domain.Candidate, error) {
	rows, err := s.DB.Query(
		`SELECT c.id, c.candidate_set_id, c.lens, c.verdict, c.rationale, c.content, c.agent_identity, c.chosen
		 FROM candidates c
		 JOIN candidate_sets cs ON c.candidate_set_id = cs.id
		 WHERE cs.comment_id=? ORDER BY c.created_at, c.rowid`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Candidate{}
	for rows.Next() {
		c, err := scanCandidate(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) MarkCandidateChosen(id string) error {
	_, err := s.DB.Exec(`UPDATE candidates SET chosen=1 WHERE id=?`, id)
	return err
}

func (s *Store) RecordSynthesis(syn domain.Synthesis) (domain.Synthesis, error) {
	if syn.ID == "" {
		syn.ID = domain.NewID()
	}
	_, err := s.DB.Exec(
		`INSERT INTO syntheses(id, candidate_set_id, agreement_score, dissent, suggestion_id, created_by)
		 VALUES(?,?,?,?,?,?)`,
		syn.ID, syn.CandidateSetID, syn.AgreementScore, syn.Dissent, syn.SuggestionID, syn.CreatedBy)
	return syn, err
}

// GetSynthesisByComment returns the latest synthesis for a comment's set, if any.
func (s *Store) GetSynthesisByComment(commentID string) (domain.Synthesis, bool, error) {
	var syn domain.Synthesis
	err := s.DB.QueryRow(
		`SELECT sy.id, sy.candidate_set_id, sy.agreement_score, sy.dissent, sy.suggestion_id, sy.created_by
		 FROM syntheses sy JOIN candidate_sets cs ON sy.candidate_set_id = cs.id
		 WHERE cs.comment_id=? ORDER BY sy.created_at DESC, sy.rowid DESC LIMIT 1`, commentID).
		Scan(&syn.ID, &syn.CandidateSetID, &syn.AgreementScore, &syn.Dissent, &syn.SuggestionID, &syn.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Synthesis{}, false, nil
	}
	return syn, err == nil, err
}

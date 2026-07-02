package store

import (
	"database/sql"
	"time"

	"github.com/rajanrx/outbox-md/internal/domain"
)

func scanComment(scan func(...any) error) (domain.Comment, error) {
	var c domain.Comment
	var pa int
	var pu, ca sql.NullString
	err := scan(&c.ID, &c.DocID, &c.AgainstVersionID, &c.Anchor.Start, &c.Anchor.End,
		&c.AuthorIdentity, &c.Owner, &c.Status, &c.ClaimToken, &pa, &pu, &ca)
	c.PostApproval = pa != 0
	if err == nil && pu.Valid && pu.String != "" {
		if t, perr := time.Parse(time.RFC3339Nano, pu.String); perr == nil {
			c.ProcessingUntil = &t
		}
	}
	if err == nil && ca.Valid && ca.String != "" {
		if t, perr := time.Parse(time.RFC3339Nano, ca.String); perr == nil {
			c.ClaimedAt = &t
		}
	}
	return c, err
}

const commentCols = `id, doc_id, against_version_id, anchor_start, anchor_end,
	author_identity, owner, status, claim_token, post_approval, processing_until, claimed_at`

// StaleClaimGrace is how long a claim may sit un-heart-beated before
// list_open_comments treats it as ABANDONED and re-surfaces it. It bounds the
// window in which a just-made claim (before the claiming agent's first
// mark_processing) is protected from being resurfaced and double-worked. It is
// set to the full processing window (matching service.DefaultProcessingTTL =
// 180s) rather than the shorter received-ack: this is strictly safer against
// double-work (a genuinely stranded claim was made minutes/hours ago, so a
// larger grace never delays its recovery) while comfortably covering the time a
// single comment can legitimately be worked before the first heartbeat.
const StaleClaimGrace = 180 * time.Second

// nullTime renders a *time.Time for storage: a null column when nil, otherwise a
// UTC RFC3339Nano string (the format scanComment parses back).
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Store) CreateComment(c domain.Comment) (domain.Comment, error) {
	if c.ID == "" {
		c.ID = domain.NewID()
	}
	pa := 0
	if c.PostApproval {
		pa = 1
	}
	_, err := s.DB.Exec(`INSERT INTO comments(`+commentCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.DocID, c.AgainstVersionID, c.Anchor.Start, c.Anchor.End,
		c.AuthorIdentity, c.Owner, c.Status, c.ClaimToken, pa,
		nullTime(c.ProcessingUntil), nullTime(c.ClaimedAt))
	return c, err
}

// SetProcessingUntil sets (or, with nil, clears) the ephemeral processing hint on
// a comment. It never touches status — "processing" is a self-expiring timestamp,
// not a lifecycle state.
func (s *Store) SetProcessingUntil(commentID string, until *time.Time) error {
	_, err := s.DB.Exec(`UPDATE comments SET processing_until=? WHERE id=?`,
		nullTime(until), commentID)
	return err
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
	out := []domain.Comment{}
	for rows.Next() {
		c, err := scanComment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListOpenComments returns the agent work set as of now: every comment that is
// 'open', PLUS every 'claimed' comment whose claim has been ABANDONED (a stale
// claim — see domain.Comment.IsStaleClaim). This recovers comments stranded when
// an agent claimed a burst but finished only some (the rest sit 'claimed' with
// no live heartbeat): a stale claim re-enters the work set on the next run,
// while a claim being actively heart-beated (live mark_processing) or made
// within StaleClaimGrace stays out, so no comment is double-worked. now is
// injected (not read from the clock here) so the staleness predicate is
// deterministic under test.
func (s *Store) ListOpenComments(now time.Time) ([]domain.Comment, error) {
	// Pull open + claimed rows, then apply the staleness predicate in Go against
	// the parsed timestamps — this avoids SQL string-comparison pitfalls between
	// the RFC3339Nano hints and any other column format. The claimed set is small.
	rows, err := s.DB.Query(`SELECT ` + commentCols +
		` FROM comments WHERE status IN ('open','claimed') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Comment{}
	for rows.Next() {
		c, err := scanComment(rows.Scan)
		if err != nil {
			return nil, err
		}
		if c.Status == domain.CommentClaimed && !c.IsStaleClaim(now, StaleClaimGrace) {
			continue // live or freshly-made claim — not our work
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCommentStatus(id string, status domain.CommentStatus, claimToken string) error {
	// Stamp claimed_at when (re-)entering 'claimed' so stale-claim recovery can
	// tell a just-made claim from an abandoned one. Other transitions leave it
	// untouched (it is only read while status == claimed). A re-claim after a
	// requeue re-stamps it, so the grace resets per claim.
	if status == domain.CommentClaimed {
		claimedAt := time.Now().UTC().Format(time.RFC3339Nano)
		_, err := s.DB.Exec(`UPDATE comments SET status=?, claim_token=?, claimed_at=? WHERE id=?`,
			status, claimToken, claimedAt, id)
		return err
	}
	_, err := s.DB.Exec(`UPDATE comments SET status=?, claim_token=? WHERE id=?`, status, claimToken, id)
	return err
}

// ClaimCommentCAS atomically claims commentID for token, succeeding only if the
// row is still CLAIMABLE right now: it is 'open', OR it is a 'claimed' row whose
// claim has gone stale (abandoned — see domain.Comment.IsStaleClaim). It is the
// concurrency-safe claim primitive behind service.Claim: with a bounded pool of
// N agents running per project, two agents must NEVER both win the same comment.
//
// The task's reference SQL folds the staleness test into the UPDATE's WHERE as a
// literal time predicate. We deliberately do NOT do that: claim timestamps are
// stored as RFC3339Nano strings whose trailing-zero trimming makes SQL
// string-comparison unsound (exactly why ListOpenComments computes staleness in
// Go, not SQL). Instead we (1) read the row, (2) decide claimability in Go via
// the SAME IsStaleClaim/StaleClaimGrace/now semantics ListOpenComments uses — so
// the claimable set equals the set an agent was offered — then (3) issue a
// conditional UPDATE guarded by a compare-and-swap on the EXACT prior
// (status, claim_token) we read. That CAS is the atomicity: any concurrent
// winner (or an interleaved accept/reply/resolve) changes at least one of those
// two columns, so the loser's UPDATE matches zero rows. The UPDATE is a
// standalone autocommit statement (no read snapshot held across it), so its
// WHERE re-evaluates against committed state; SetMaxOpenConns(1) serializes the
// two writes, so the loser sees a clean 0 rows-affected, never SQLITE_BUSY.
//
// It returns won=true iff this call claimed the row (rows-affected == 1); won=false
// means the row was not claimable or was lost to a concurrent claim (a skip, not
// an error). A genuine store error is returned as err.
func (s *Store) ClaimCommentCAS(commentID, token string, now time.Time, grace time.Duration) (bool, error) {
	c, err := s.GetComment(commentID)
	if err != nil {
		return false, err
	}
	claimable := c.Status == domain.CommentOpen ||
		(c.Status == domain.CommentClaimed && c.IsStaleClaim(now, grace))
	if !claimable {
		return false, nil
	}
	claimedAt := now.UTC().Format(time.RFC3339Nano)
	res, err := s.DB.Exec(
		`UPDATE comments SET status=?, claim_token=?, claimed_at=?
		 WHERE id=? AND status=? AND claim_token=?`,
		domain.CommentClaimed, token, claimedAt, commentID, c.Status, c.ClaimToken)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// RequeueClaimedCommentsForProject resets every 'claimed' comment in project
// back to 'open', clearing the claim (token, processing hint, claimed_at) so the
// comment re-enters the agent work set immediately. It is the store primitive
// behind `outbox retry`: a claimed-but-unfinished comment is by definition not
// done, so re-queuing all claims for a project is safe. It returns how many rows
// were re-queued. claim_token is cleared to ” (not NULL) because scanComment
// reads it as a plain string; the timestamps are nulled so a fresh claim
// re-stamps them. The empty project name targets single-folder-mode comments.
func (s *Store) RequeueClaimedCommentsForProject(project string) (int, error) {
	res, err := s.DB.Exec(
		`UPDATE comments SET status=?, claim_token='', processing_until=NULL, claimed_at=NULL
		 WHERE status=? AND doc_id IN (SELECT id FROM documents WHERE project=?)`,
		domain.CommentOpen, domain.CommentClaimed, project)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (s *Store) UpdateCommentAnchor(id string, a domain.Anchor, status domain.CommentStatus) error {
	_, err := s.DB.Exec(`UPDATE comments SET anchor_start=?, anchor_end=?, status=? WHERE id=?`,
		a.Start, a.End, status, id)
	return err
}

// ReopenCommentIfNotResolved re-queues a comment to open, but only if it has
// not already been resolved. This makes a losing/duplicate accept's requeue a
// no-op once a winning accept has resolved the same comment.
func (s *Store) ReopenCommentIfNotResolved(id string) error {
	_, err := s.DB.Exec(`UPDATE comments SET status=?, claim_token='' WHERE id=? AND status != ?`,
		domain.CommentOpen, id, domain.CommentResolved)
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

func (s *Store) ListThread(commentID string) ([]domain.ThreadMessage, error) {
	rows, err := s.DB.Query(
		`SELECT id, comment_id, author_identity, body FROM thread_messages
		 WHERE comment_id=? ORDER BY created_at`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ThreadMessage{}
	for rows.Next() {
		var m domain.ThreadMessage
		if err := rows.Scan(&m.ID, &m.CommentID, &m.AuthorIdentity, &m.Body); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

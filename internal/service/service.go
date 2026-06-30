package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/rajanrx/outbox-md/internal/anchor"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
	"github.com/rajanrx/outbox-md/internal/webhook"
)

type Service struct {
	store     *store.Store
	writeFile func(path, content string) error
	cfg       config.Config
	notify    webhook.Notifier
}

func New(st *store.Store, writeFile func(path, content string) error) *Service {
	return &Service{store: st, writeFile: writeFile, cfg: config.Defaults(), notify: webhook.Nop{}}
}

// SetConfig replaces the effective configuration (called once at startup with
// the loaded outbox.yaml).
func (s *Service) SetConfig(cfg config.Config) { s.cfg = cfg }

// SetWebhook installs the notifier fired on governance events. Defaults to a
// no-op; main wires in an HTTP notifier when a webhook URL is configured.
func (s *Service) SetWebhook(n webhook.Notifier) { s.notify = n }

// fireCommentEvent builds and fires a comment-scoped webhook event. All store
// lookups are best-effort: a failed lookup just leaves its field empty, never
// blocking or failing the caller's operation. When no sink is live, it returns
// before any store read — there is nothing to consume the event.
func (s *Service) fireCommentEvent(event string, c domain.Comment) {
	if !s.notify.Enabled() {
		return
	}
	a := c.Anchor
	payload := webhook.Event{
		Event: event, DocID: c.DocID, CommentID: c.ID, Anchor: &a,
		TS: time.Now().UTC().Format(time.RFC3339),
	}
	if doc, err := s.store.GetDocument(c.DocID); err == nil {
		payload.DocPath = doc.Path
	}
	if ver, err := s.store.GetVersion(c.AgainstVersionID); err == nil {
		payload.Excerpt = anchor.Excerpt(ver.Content, c.Anchor.Start, c.Anchor.End)
	}
	if thread, err := s.store.ListThread(c.ID); err == nil {
		payload.Thread = thread
	}
	s.notify.Fire(event, payload)
}

// fireDocApproved fires a document.approved event carrying only the document
// identity (no comment/anchor/thread). It short-circuits when no sink is live.
func (s *Service) fireDocApproved(doc domain.Document) {
	if !s.notify.Enabled() {
		return
	}
	s.notify.Fire(webhook.EventDocumentApprove, webhook.Event{
		Event: webhook.EventDocumentApprove, DocID: doc.ID, DocPath: doc.Path,
		TS: time.Now().UTC().Format(time.RFC3339),
	})
}

// unresolvedCount returns the number of comments on docID that are not resolved.
// Approval is gated on this being zero (see Approve/Reapprove).
func (s *Service) unresolvedCount(docID string) (int, error) {
	comments, err := s.store.ListComments(docID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range comments {
		if c.Status != domain.CommentResolved {
			n++
		}
	}
	return n, nil
}

// Config returns the effective configuration (read-only view for the API).
func (s *Service) Config() config.Config { return s.cfg }

func (s *Service) PostComment(docID string, a domain.Anchor, author string) (domain.Comment, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Comment{}, err
	}
	governed := doc.Status == domain.DocApproved || doc.Status == domain.DocAmending
	if governed && !s.cfg.Approval.PostApprovalComments {
		return domain.Comment{}, errors.New("comments are disabled on approved documents")
	}
	c, err := s.store.CreateComment(domain.Comment{
		DocID: docID, AgainstVersionID: doc.CurrentVersionID, Anchor: a,
		AuthorIdentity: author, Owner: author, Status: domain.CommentOpen,
		PostApproval: governed,
	})
	if err != nil {
		return domain.Comment{}, err
	}
	s.fireCommentEvent(webhook.EventCommentCreated, c)
	return c, nil
}

func (s *Service) Claim(commentIDs []string, agent string) (string, error) {
	if len(commentIDs) > s.cfg.Agent.BatchSize {
		return "", fmt.Errorf("batch size exceeded: at most %d comments per claim", s.cfg.Agent.BatchSize)
	}
	token := domain.NewID()
	for _, id := range commentIDs {
		if err := s.store.UpdateCommentStatus(id, domain.CommentClaimed, token); err != nil {
			return "", err
		}
	}
	return token, nil
}

func (s *Service) requireToken(commentID, token string) (domain.Comment, error) {
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return domain.Comment{}, err
	}
	if c.ClaimToken == "" || c.ClaimToken != token {
		return domain.Comment{}, errors.New("invalid or missing claim token")
	}
	return c, nil
}

func (s *Service) Propose(commentID, token, content, agent string) (domain.Suggestion, error) {
	c, err := s.requireToken(commentID, token)
	if err != nil {
		return domain.Suggestion{}, err
	}
	sg, err := s.store.CreateSuggestion(domain.Suggestion{
		CommentID: commentID, AgainstVersionID: c.AgainstVersionID,
		ProposedContent: content, State: domain.SuggestionProposed, CreatedBy: agent,
	})
	if err != nil {
		return domain.Suggestion{}, err
	}
	_ = s.store.UpdateCommentStatus(commentID, domain.CommentAddressed, c.ClaimToken)
	return sg, nil
}

func (s *Service) Reply(commentID, token, body, agent string) error {
	c, err := s.requireToken(commentID, token)
	if err != nil {
		return err
	}
	if _, err := s.store.AddThreadMessage(domain.ThreadMessage{
		CommentID: commentID, AuthorIdentity: agent, Body: body,
	}); err != nil {
		return err
	}
	return s.store.UpdateCommentStatus(commentID, domain.CommentReplied, c.ClaimToken)
}

func (s *Service) HumanReply(commentID, body string) (domain.ThreadMessage, error) {
	m, err := s.store.AddThreadMessage(domain.ThreadMessage{
		CommentID: commentID, AuthorIdentity: "human", Body: body,
	})
	if err != nil {
		return m, err
	}
	// A further human reply must re-surface the comment for the agent: an agent
	// that already claimed/replied has dropped it from its outbox, and
	// list_open_comments only returns 'open'. Re-queue it (unless already
	// resolved) so the agent responds again.
	_ = s.store.ReopenCommentIfNotResolved(commentID)
	// Fire unconditionally — a reply happened even when the reopen was a no-op
	// (e.g. the comment was already resolved).
	if c, err := s.store.GetComment(commentID); err == nil {
		s.fireCommentEvent(webhook.EventCommentReplied, c)
	}
	return m, nil
}

// LocalHuman is the identity of the single local reviewer. This local-first
// app has no authentication, so the caller's identity is server-set rather than
// taken from the request. When auth is added, this becomes the identity derived
// from the request's verified auth context.
const LocalHuman = "human"

// Resolve closes a comment owned by the local human. The caller identity is NOT
// accepted from the request (which would be spoofable) — it is fixed to the
// single local reviewer here.
func (s *Service) Resolve(commentID string) error {
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return err
	}
	if c.Owner != LocalHuman {
		return errors.New("only the comment owner may resolve it")
	}
	if err := s.store.UpdateCommentStatus(commentID, domain.CommentResolved, ""); err != nil {
		return err
	}
	c.Status = domain.CommentResolved
	s.fireCommentEvent(webhook.EventCommentResolved, c)
	return nil
}

func (s *Service) RejectSuggestion(commentID string) error {
	sg, ok, err := s.store.GetSuggestionByComment(commentID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no suggestion to reject")
	}
	_ = s.store.UpdateSuggestionState(sg.ID, domain.SuggestionRejected)
	return s.store.UpdateCommentStatus(commentID, domain.CommentOpen, "")
}

func (s *Service) Accept(commentID string) (domain.Version, error) {
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return domain.Version{}, err
	}
	if c.Status == domain.CommentResolved {
		return domain.Version{}, errors.New("comment already resolved")
	}
	sg, ok, err := s.store.GetSuggestionByComment(commentID)
	if err != nil {
		return domain.Version{}, err
	}
	if !ok {
		return domain.Version{}, errors.New("no suggestion to accept")
	}
	if sg.State != domain.SuggestionProposed {
		return domain.Version{}, errors.New("suggestion is not in proposed state")
	}
	doc, err := s.store.GetDocument(c.DocID)
	if err != nil {
		return domain.Version{}, err
	}
	// Reject stale applies: a suggestion proposed against an older version must
	// not clobber a newer accepted edit. The agent must re-propose against the
	// current version, so the comment returns to the outbox.
	if sg.AgainstVersionID != doc.CurrentVersionID {
		// Conditional requeue: never override a state a concurrent winning
		// accept may have already set to accepted/resolved.
		_ = s.store.RejectSuggestionIfProposed(sg.ID)
		_ = s.store.ReopenCommentIfNotResolved(commentID)
		return domain.Version{}, errors.New("suggestion is stale: proposed against an older version")
	}
	oldVer, err := s.store.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return domain.Version{}, err
	}

	// Record the new version and write the file inside one transaction, guarded
	// by a compare-and-swap on the current version (oldVer.ID). This serializes
	// concurrent accepts so two requests cannot both advance past the same
	// version. The write runs only for the winning accept; if it ran but a later
	// step failed, we compensate the on-disk file back to the current version.
	// The invariant — the file on disk equals the current version's content —
	// holds whether Accept succeeds or fails.
	// draft writes the file directly; an approved/amending doc accumulates the
	// new version ahead of the baseline and leaves the on-disk file untouched
	// (the baseline) until re-approval.
	governed := doc.Status == domain.DocApproved || doc.Status == domain.DocAmending
	wrote := false
	var newVer domain.Version
	if governed {
		// A governed accept accumulates the new version ahead of the baseline and
		// flips status to amending inside the version transaction — CAS-guarded on
		// the current pointer and never touching approved_version_id. It writes no
		// disk (the on-disk file stays the baseline until re-approval).
		newVer, err = s.store.AddGovernedVersionTx(doc.ID, oldVer.ID, sg.ProposedContent, sg.CreatedBy)
	} else {
		writeFn := func(v domain.Version) error {
			wrote = true
			return s.writeFile(doc.Path, v.Content)
		}
		newVer, err = s.store.AddVersionTx(doc.ID, oldVer.ID, sg.ProposedContent, sg.CreatedBy, writeFn)
	}
	if err != nil {
		if wrote {
			_ = s.writeFile(doc.Path, oldVer.Content)
		}
		if errors.Is(err, store.ErrVersionConflict) {
			// Lost the race: re-queue so the agent can re-propose against the new
			// current version — but conditionally, so we never flip a suggestion
			// the winning accept already marked accepted (same-comment duplicate
			// accept) back to rejected/open. Governed accepts requeue identically.
			_ = s.store.RejectSuggestionIfProposed(sg.ID)
			_ = s.store.ReopenCommentIfNotResolved(commentID)
		}
		return domain.Version{}, err
	}
	_ = s.store.UpdateSuggestionState(sg.ID, domain.SuggestionAccepted)
	_ = s.store.UpdateCommentStatus(commentID, domain.CommentResolved, "")

	comments, err := s.store.ListComments(doc.ID)
	if err != nil {
		return domain.Version{}, err
	}
	for _, oc := range comments {
		if oc.ID == commentID || oc.AgainstVersionID != oldVer.ID {
			continue
		}
		if oc.Status == domain.CommentResolved || oc.Status == domain.CommentDetached {
			continue
		}
		na, ok := anchor.Remap(oldVer.Content, newVer.Content, oc.Anchor)
		if !ok {
			_ = s.store.UpdateCommentStatus(oc.ID, domain.CommentDetached, oc.ClaimToken)
			continue
		}
		_ = s.store.RebaseComment(oc.ID, newVer.ID, na, oc.Status)
	}
	return newVer, nil
}

// Approve pins the current version as the approved baseline. Valid only from
// draft; the on-disk file already equals the current version, so no rewrite is
// needed. Identity is server-set (LocalHuman), never taken from the request.
func (s *Service) Approve(docID, note string) (domain.Approval, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Approval{}, err
	}
	if doc.Status != domain.DocDraft {
		return domain.Approval{}, errors.New("already approved — use re-approve")
	}
	// Gate: every comment must be resolved before the baseline can be pinned.
	if n, err := s.unresolvedCount(docID); err != nil {
		return domain.Approval{}, err
	} else if n > 0 {
		return domain.Approval{}, fmt.Errorf("cannot approve: %d unresolved comment(s) — resolve all comments first", n)
	}
	// CAS-guarded pin: only pin the baseline if the current pointer is still the
	// version we read. A concurrent draft accept that advanced current between the
	// read above and this pin makes the guard fail, so we never pin a stale
	// baseline behind the new current.
	if err := s.store.SetDocumentApprovalIfCurrent(docID, doc.CurrentVersionID, doc.CurrentVersionID, domain.DocApproved); err != nil {
		if errors.Is(err, store.ErrVersionConflict) {
			return domain.Approval{}, errors.New("document changed during approval; retry")
		}
		return domain.Approval{}, err
	}
	app, err := s.store.CreateApproval(domain.Approval{
		DocID: docID, VersionID: doc.CurrentVersionID, ApprovedBy: LocalHuman, Note: note,
	})
	if err != nil {
		return domain.Approval{}, err
	}
	s.fireDocApproved(doc)
	return app, nil
}

// Reapprove advances the baseline to the working head and writes it to disk.
// Valid only while amending with pending changes ahead of the baseline.
func (s *Service) Reapprove(docID, note string) (domain.Approval, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Approval{}, err
	}
	if doc.Status != domain.DocAmending || doc.CurrentVersionID == doc.ApprovedVersionID {
		return domain.Approval{}, errors.New("nothing to re-approve")
	}
	// Gate: every comment must be resolved before the baseline advances.
	if n, err := s.unresolvedCount(docID); err != nil {
		return domain.Approval{}, err
	} else if n > 0 {
		return domain.Approval{}, fmt.Errorf("cannot approve: %d unresolved comment(s) — resolve all comments first", n)
	}
	ver, err := s.store.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return domain.Approval{}, err
	}
	// Advance the baseline and write the head to disk inside one CAS-guarded tx.
	// If a concurrent accept moved the current pointer under us the CAS fails and
	// nothing is written. Capture the prior baseline content so we can compensate
	// the on-disk file if the write ran but the commit then failed.
	oldBaseline, err := s.store.GetVersion(doc.ApprovedVersionID)
	if err != nil {
		return domain.Approval{}, err
	}
	wrote := false
	err = s.store.ReapproveTx(docID, doc.CurrentVersionID, ver.Content, func() error {
		wrote = true
		return s.writeFile(doc.Path, ver.Content)
	})
	if err != nil {
		if wrote {
			_ = s.writeFile(doc.Path, oldBaseline.Content)
		}
		if errors.Is(err, store.ErrVersionConflict) {
			return domain.Approval{}, errors.New("document changed during re-approval; retry")
		}
		return domain.Approval{}, err
	}
	app, err := s.store.CreateApproval(domain.Approval{
		DocID: docID, VersionID: doc.CurrentVersionID, ApprovedBy: LocalHuman, Note: note,
	})
	if err != nil {
		return domain.Approval{}, err
	}
	s.fireDocApproved(doc)
	return app, nil
}

package service

import (
	"errors"

	"github.com/rajanrx/outbox-md/internal/anchor"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/store"
)

type Service struct {
	store     *store.Store
	writeFile func(path, content string) error
}

func New(st *store.Store, writeFile func(path, content string) error) *Service {
	return &Service{store: st, writeFile: writeFile}
}

func (s *Service) PostComment(docID string, a domain.Anchor, author string) (domain.Comment, error) {
	doc, err := s.store.GetDocument(docID)
	if err != nil {
		return domain.Comment{}, err
	}
	return s.store.CreateComment(domain.Comment{
		DocID: docID, AgainstVersionID: doc.CurrentVersionID, Anchor: a,
		AuthorIdentity: author, Owner: author, Status: domain.CommentOpen,
	})
}

func (s *Service) Claim(commentIDs []string, agent string) (string, error) {
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
	wrote := false
	newVer, err := s.store.AddVersionTx(doc.ID, oldVer.ID, sg.ProposedContent, sg.CreatedBy, func(v domain.Version) error {
		wrote = true
		return s.writeFile(doc.Path, v.Content)
	})
	if err != nil {
		if wrote {
			_ = s.writeFile(doc.Path, oldVer.Content)
		}
		if errors.Is(err, store.ErrVersionConflict) {
			// Lost the race: re-queue so the agent can re-propose against the new
			// current version — but conditionally, so we never flip a suggestion
			// the winning accept already marked accepted (same-comment duplicate
			// accept) back to rejected/open.
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

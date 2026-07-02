package mcp

import (
	"fmt"
	"time"

	"github.com/rajanrx/outbox-md/internal/anchor"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

type Handlers struct {
	Svc *service.Service
	St  *store.Store
}

func (h *Handlers) ReadDoc(docID string) (map[string]any, error) {
	doc, err := h.St.GetDocument(docID)
	if err != nil {
		return nil, err
	}
	// Enforce the sources whitelist on the MCP surface too: a doc outside its
	// project's active whitelist is hidden from agents, mirroring the HTTP API's
	// 404. The check is project-aware — each project's own Sources gate its docs.
	if h.Svc.SourcesRestricted() && !h.Svc.ProjectServes(doc.Project, doc.Path) {
		return nil, fmt.Errorf("document %s not found", docID)
	}
	ver, err := h.St.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	// Surface the project so an agent can tell which project this doc belongs to
	// (also present inside document.project; echoed at top level for convenience).
	return map[string]any{"document": doc, "content": ver.Content, "project": doc.Project}, nil
}

// OpenComment enriches a queued comment with the context an agent needs to act
// on it: the document path, the anchored excerpt (the text the comment refers
// to), and the thread (the human's feedback is stored as the first message).
type OpenComment struct {
	domain.Comment
	Project string                 `json:"project"` // the project the comment's doc belongs to ("" = single-folder mode)
	DocPath string                 `json:"docPath"`
	Excerpt string                 `json:"excerpt"` // the anchored text the comment refers to
	Thread  []domain.ThreadMessage `json:"thread"`  // the human's feedback (and any prior discussion)
}

func (h *Handlers) ListOpenComments() ([]OpenComment, error) {
	comments, err := h.St.ListOpenComments(time.Now().UTC())
	if err != nil {
		return nil, err
	}
	restricted := h.Svc.SourcesRestricted()
	out := make([]OpenComment, 0, len(comments))
	for _, c := range comments {
		oc := OpenComment{Comment: c, Thread: []domain.ThreadMessage{}}
		// Don't fail the whole list if one lookup errors — just skip that field.
		if doc, err := h.St.GetDocument(c.DocID); err == nil {
			// Hide comments on docs outside their project's sources whitelist from
			// agents, mirroring the HTTP list filter (a resolvable doc that isn't
			// served is skipped; an unresolvable lookup falls through, as before).
			if restricted && !h.Svc.ProjectServes(doc.Project, doc.Path) {
				continue
			}
			oc.DocPath = doc.Path
			oc.Project = doc.Project
		}
		if ver, err := h.St.GetVersion(c.AgainstVersionID); err == nil {
			oc.Excerpt = anchor.Excerpt(ver.Content, c.Anchor.Start, c.Anchor.End)
		}
		if thread, err := h.St.ListThread(c.ID); err == nil && thread != nil {
			oc.Thread = thread
		}
		out = append(out, oc)
	}
	return out, nil
}

// served reports whether commentID belongs to a doc inside the active sources
// whitelist. An unknown comment or doc → false (deny), matching read_doc. Every
// comment-scoped MCP write gates on this so an agent can neither discover
// (list_open_comments/read_doc) nor mutate a hidden doc via a stale id.
func (h *Handlers) served(commentID string) bool {
	if !h.Svc.SourcesRestricted() {
		return true
	}
	c, err := h.St.GetComment(commentID)
	if err != nil {
		return false
	}
	doc, err := h.St.GetDocument(c.DocID)
	if err != nil {
		return false
	}
	return h.Svc.ProjectServes(doc.Project, doc.Path)
}

func (h *Handlers) ClaimComment(ids []string, agent string) (string, error) {
	for _, id := range ids {
		if !h.served(id) {
			return "", fmt.Errorf("comment %s not found", id)
		}
	}
	return h.Svc.Claim(ids, agent)
}

func (h *Handlers) ProposeSuggestion(commentID, token, content, agent string) (domain.Suggestion, error) {
	if !h.served(commentID) {
		return domain.Suggestion{}, fmt.Errorf("comment %s not found", commentID)
	}
	return h.Svc.Propose(commentID, token, content, agent)
}

func (h *Handlers) ReplyInThread(commentID, token, body, agent string) error {
	if !h.served(commentID) {
		return fmt.Errorf("comment %s not found", commentID)
	}
	return h.Svc.Reply(commentID, token, body, agent)
}

// MarkProcessing flags a claimed comment as being worked on by the agent, so the
// human sees it live. ttlSeconds <= 0 uses the service default; re-calling
// heartbeats (extends) the deadline. Returns the deadline as RFC3339.
func (h *Handlers) MarkProcessing(commentID, token string, ttlSeconds int) (string, error) {
	if !h.served(commentID) {
		return "", fmt.Errorf("comment %s not found", commentID)
	}
	until, err := h.Svc.MarkProcessing(commentID, token, time.Duration(ttlSeconds)*time.Second)
	if err != nil {
		return "", err
	}
	return until.Format(time.RFC3339), nil
}

// SubmitReview is the council-mode sibling of ProposeSuggestion: it records one
// member's review as a Candidate instead of a single baseline suggestion. It
// never resolves or writes disk — picking/accepting stay human-only.
func (h *Handlers) SubmitReview(commentID, token, lens, verdict, rationale, content, agentIdentity string) (domain.Candidate, error) {
	if !h.served(commentID) {
		return domain.Candidate{}, fmt.Errorf("comment %s not found", commentID)
	}
	return h.Svc.SubmitReview(commentID, token, lens, verdict, rationale, content, agentIdentity)
}

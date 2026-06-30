package mcp

import (
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
	ver, err := h.St.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"document": doc, "content": ver.Content}, nil
}

// OpenComment enriches a queued comment with the context an agent needs to act
// on it: the document path, the anchored excerpt (the text the comment refers
// to), and the thread (the human's feedback is stored as the first message).
type OpenComment struct {
	domain.Comment
	DocPath string                 `json:"docPath"`
	Excerpt string                 `json:"excerpt"` // the anchored text the comment refers to
	Thread  []domain.ThreadMessage `json:"thread"`  // the human's feedback (and any prior discussion)
}

func (h *Handlers) ListOpenComments() ([]OpenComment, error) {
	comments, err := h.St.ListOpenComments()
	if err != nil {
		return nil, err
	}
	out := make([]OpenComment, 0, len(comments))
	for _, c := range comments {
		oc := OpenComment{Comment: c, Thread: []domain.ThreadMessage{}}
		// Don't fail the whole list if one lookup errors — just skip that field.
		if doc, err := h.St.GetDocument(c.DocID); err == nil {
			oc.DocPath = doc.Path
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

func (h *Handlers) ClaimComment(ids []string, agent string) (string, error) {
	return h.Svc.Claim(ids, agent)
}

func (h *Handlers) ProposeSuggestion(commentID, token, content, agent string) (domain.Suggestion, error) {
	return h.Svc.Propose(commentID, token, content, agent)
}

func (h *Handlers) ReplyInThread(commentID, token, body, agent string) error {
	return h.Svc.Reply(commentID, token, body, agent)
}

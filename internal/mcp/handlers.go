package mcp

import (
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

func (h *Handlers) ListOpenComments() ([]domain.Comment, error) {
	return h.St.ListOpenComments()
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

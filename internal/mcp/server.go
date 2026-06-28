package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajanrx/outbox-md/internal/domain"
)

// NewServer registers the five v1-core tools backed by h and returns the
// SDK server. Mount it over HTTP with mcp.NewStreamableHTTPHandler, or run
// it over stdio with Server.Run.
func NewServer(h *Handlers) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "outbox-md", Version: "0.1.0"}, nil)

	type readDocIn struct {
		DocID string `json:"docId" jsonschema:"the document id to read"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_doc",
		Description: "Read the current content and metadata of a document.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in readDocIn) (*mcp.CallToolResult, map[string]any, error) {
		out, err := h.ReadDoc(in.DocID)
		return nil, out, err
	})

	type listOut struct {
		Comments []domain.Comment `json:"comments"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_open_comments",
		Description: "List the ordered outbox of open comments awaiting processing.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listOut, error) {
		cs, err := h.ListOpenComments()
		return nil, listOut{Comments: cs}, err
	})

	type claimIn struct {
		CommentIDs []string `json:"commentIds" jsonschema:"ids of comments to claim"`
		Agent      string   `json:"agent" jsonschema:"the agent identity"`
	}
	type claimOut struct {
		Token string `json:"token"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "claim_comment",
		Description: "Claim one or more comments for processing; returns a claim token.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult, claimOut, error) {
		tok, err := h.ClaimComment(in.CommentIDs, in.Agent)
		return nil, claimOut{Token: tok}, err
	})

	type proposeIn struct {
		CommentID string `json:"commentId"`
		Token     string `json:"token" jsonschema:"the claim token from claim_comment"`
		Content   string `json:"content" jsonschema:"the full proposed replacement content"`
		Agent     string `json:"agent"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "propose_suggestion",
		Description: "Propose a tracked-change edit (full replacement content) for a claimed comment.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in proposeIn) (*mcp.CallToolResult, domain.Suggestion, error) {
		sg, err := h.ProposeSuggestion(in.CommentID, in.Token, in.Content, in.Agent)
		return nil, sg, err
	})

	type replyIn struct {
		CommentID string `json:"commentId"`
		Token     string `json:"token"`
		Body      string `json:"body"`
		Agent     string `json:"agent"`
	}
	type replyOut struct {
		OK bool `json:"ok"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "reply_in_thread",
		Description: "Reply in a comment thread (counter, clarify, or discuss) for a claimed comment.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in replyIn) (*mcp.CallToolResult, replyOut, error) {
		err := h.ReplyInThread(in.CommentID, in.Token, in.Body, in.Agent)
		return nil, replyOut{OK: err == nil}, err
	})

	return s
}

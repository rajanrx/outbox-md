package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
)

// NewServer registers the v1-core tools (plus the council-mode submit_review)
// backed by h and returns the SDK server. Mount it over HTTP with
// mcp.NewStreamableHTTPHandler, or run it over stdio with Server.Run.
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
		Comments []OpenComment `json:"comments"`
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
		// Claimed is the subset of the requested ids this call actually won. Under
		// fan-out (multiple agents at once) another agent may claim an id first; a
		// lost id is absent here. Process ONLY these ids with this token; skip the
		// rest — they are being handled by another agent.
		Claimed []string `json:"claimed"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "claim_comment",
		Description: "Claim one or more comments for processing; returns a claim token and the subset of ids actually won (others were claimed by a concurrent agent — process only the won ids).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in claimIn) (*mcp.CallToolResult, claimOut, error) {
		tok, claimed, err := h.ClaimComment(in.CommentIDs, in.Agent)
		return nil, claimOut{Token: tok, Claimed: claimed}, err
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

	type submitReviewIn struct {
		CommentID     string `json:"commentId"`
		Token         string `json:"token" jsonschema:"the claim token from claim_comment"`
		Lens          string `json:"lens" jsonschema:"the review lens: correctness | completeness | ambiguity | risk | simplicity | skeptic"`
		Verdict       string `json:"verdict" jsonschema:"the member's stance: edit | reply | reject_comment"`
		Rationale     string `json:"rationale" jsonschema:"why, in the member's own words"`
		Content       string `json:"content,omitempty" jsonschema:"the full proposed replacement content; required iff verdict == edit"`
		AgentIdentity string `json:"agentIdentity" jsonschema:"the council member's identity"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "submit_review",
		Description: "Council-mode sibling of propose_suggestion: record one member's independent review (lens + verdict + rationale, plus full replacement content iff verdict is edit) as a candidate. Never resolves or writes; the human picks.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in submitReviewIn) (*mcp.CallToolResult, domain.Candidate, error) {
		cd, err := h.SubmitReview(in.CommentID, in.Token, in.Lens, in.Verdict, in.Rationale, in.Content, in.AgentIdentity)
		return nil, cd, err
	})

	type listCandidatesIn struct {
		CommentID string `json:"commentId" jsonschema:"the comment whose council to read"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_candidates",
		Description: "Council chair read: the candidate set plus every member's independent candidate (round, lens, verdict, rationale, edit content, identity) and the synthesis if one was recorded. Use it to weigh the members' takes before recording a verdict.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in listCandidatesIn) (*mcp.CallToolResult, service.CouncilView, error) {
		v, err := h.ListCandidates(in.CommentID)
		return nil, v, err
	})

	type recordSynthesisIn struct {
		CommentID      string  `json:"commentId"`
		Token          string  `json:"token" jsonschema:"the claim token from claim_comment"`
		Content        string  `json:"content,omitempty" jsonschema:"the full replacement content of the chair's verdict; when set, an accept-eligible suggestion is emitted"`
		Dissent        string  `json:"dissent,omitempty" jsonschema:"the preserved minority/skeptic position"`
		AgreementScore float64 `json:"agreementScore" jsonschema:"council agreement, 0..1"`
		Confidence     int     `json:"confidence" jsonschema:"the chair's confidence in the verdict, 0..100"`
		AgentIdentity  string  `json:"agentIdentity" jsonschema:"the council chair's identity"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "record_synthesis",
		Description: "Council chair verdict: record the roll-up of a candidate set (agreement, confidence, dissent) and — when it carries edit content — emit the accept-eligible suggestion the human reviews. Token-authed: only the claiming council may record. Returns the synthesis, including its emitted suggestionId.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in recordSynthesisIn) (*mcp.CallToolResult, domain.Synthesis, error) {
		syn, err := h.RecordSynthesis(in.CommentID, in.Token, in.Content, in.Dissent, in.AgreementScore, in.Confidence, in.AgentIdentity)
		return nil, syn, err
	})

	type markProcessingIn struct {
		CommentID  string `json:"commentId"`
		Token      string `json:"token" jsonschema:"the claim token from claim_comment"`
		TTLSeconds int    `json:"ttlSeconds,omitempty" jsonschema:"how long the hint lives, in seconds; omit or <=0 for the 180s default"`
	}
	type markProcessingOut struct {
		ProcessingUntil string `json:"processingUntil"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "mark_processing",
		Description: "Mark a claimed comment as being worked on so the human sees an 'AI processing…' indicator. Call it right after claim_comment, and again to heartbeat on long runs. Ephemeral and self-expiring (default 180s): it writes no file and changes no status.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in markProcessingIn) (*mcp.CallToolResult, markProcessingOut, error) {
		until, err := h.MarkProcessing(in.CommentID, in.Token, in.TTLSeconds)
		return nil, markProcessingOut{ProcessingUntil: until}, err
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

	// process_outbox bundles the agent workflow as an MCP prompt so a connected
	// agent can pull the playbook for working the queue instead of guessing it.
	s.AddPrompt(&mcp.Prompt{
		Name:        "process_outbox",
		Description: "The workflow for an AI agent processing the outbox of open comments on a Markdown spec.",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "How to process the outbox in outbox-md.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: processOutboxGuidance},
			}},
		}, nil
	})

	return s
}

const processOutboxGuidance = `You are processing the outbox for a Markdown spec in outbox-md. Work the queue IN ORDER and do not exceed the configured batch size.
1. Call ` + "`list_open_comments`" + ` — each item includes the anchored ` + "`excerpt`" + ` (the text the human flagged) and the ` + "`thread`" + ` (their feedback).
2. For a comment you'll act on, call ` + "`read_doc`" + ` for full context, then ` + "`claim_comment`" + `. It returns ` + "`claimed`" + ` — the subset of ids you actually WON — plus a token. Under fan-out other agents claim concurrently, so process ONLY the ids in ` + "`claimed`" + `: if a comment you intended to work on is absent from ` + "`claimed`" + `, another agent won it — SKIP it (do not act on it; its token checks would fail).
2b. Right after claiming, call ` + "`mark_processing`" + ` for a comment you WON (its id + the token) so the human sees it's being worked on. On a long run, call it again periodically to heartbeat (it self-expires after ~180s). It writes nothing and resolves nothing — just a live "AI processing…" hint that clears when you reply/propose.
3. Respond with EITHER ` + "`propose_suggestion`" + ` (a tracked-change edit — provide the FULL replacement document content) OR ` + "`reply_in_thread`" + ` (to counter, clarify, or discuss) — using the claim token and your agent identity. In council mode, submit a lensed review with ` + "`submit_review`" + ` instead (its verdict/rationale and edit content become one candidate among N); the human picks.
4. You CANNOT resolve comments, pick a candidate, or approve documents — those are human-only. Never attempt them.
Keep edits minimal and faithful to the feedback; the human reviews every suggestion before it touches the file.`

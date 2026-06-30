package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// APIBackend is the opt-in, fully-implemented reference: it connects to the
// outbox-md MCP server as a client over Streamable-HTTP, walks the open outbox,
// and uses the Anthropic Messages API to decide each response. Unlike cli mode
// it needs ANTHROPIC_API_KEY and bills per token. The Node/Python ports ship a
// stub for this mode (see README) — Go is the canonical implementation.
type APIBackend struct {
	MCPURL  string
	APIKey  string
	Model   string
	AgentID string
}

// openComment mirrors the fields of the server's OpenComment that the runner
// needs to reason about a comment. Only a subset is decoded.
type openComment struct {
	ID      string          `json:"id"`
	DocID   string          `json:"docId"`
	DocPath string          `json:"docPath"`
	Excerpt string          `json:"excerpt"`
	Thread  []threadMessage `json:"thread"`
}

type threadMessage struct {
	AuthorIdentity string `json:"authorIdentity"`
	Body           string `json:"body"`
}

// decision is the structured response the model returns for one comment.
type decision struct {
	// Action is "propose" (a tracked-change edit) or "reply" (counter/clarify).
	Action string `json:"action"`
	// Content is the FULL replacement document for "propose", or the reply text
	// for "reply".
	Content string `json:"content"`
}

// Run executes one pass of the outbox loop: list → (per comment) claim → decide
// via LLM → propose_suggestion or reply_in_thread. It never resolves, accepts,
// or approves — those are human-only and have no MCP tool.
func (b *APIBackend) Run() error {
	if b.APIKey == "" {
		return fmt.Errorf("api mode: ANTHROPIC_API_KEY is required (or use the default RUNNER_AGENT_MODE=cli)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "outbox-runner", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: b.MCPURL}, nil)
	if err != nil {
		return fmt.Errorf("api: connect MCP %s: %w", b.MCPURL, err)
	}
	defer session.Close()

	var list struct {
		Comments []openComment `json:"comments"`
	}
	if err := callTool(ctx, session, "list_open_comments", nil, &list); err != nil {
		return fmt.Errorf("api: list_open_comments: %w", err)
	}
	if len(list.Comments) == 0 {
		log.Printf("api: outbox empty, nothing to do")
		return nil
	}
	log.Printf("api: %d open comment(s)", len(list.Comments))

	for _, c := range list.Comments {
		if err := b.handleComment(ctx, session, c); err != nil {
			// One bad comment must not abort the rest of the pass.
			log.Printf("api: comment %s: %v", c.ID, err)
		}
	}
	return nil
}

// handleComment claims one comment, asks the model how to respond, and writes
// the response back through MCP.
func (b *APIBackend) handleComment(ctx context.Context, s *mcp.ClientSession, c openComment) error {
	var claim struct {
		Token string `json:"token"`
	}
	if err := callTool(ctx, s, "claim_comment", map[string]any{
		"commentIds": []string{c.ID},
		"agent":      b.AgentID,
	}, &claim); err != nil {
		return fmt.Errorf("claim: %w", err)
	}

	// Pull the full document for context so a "propose" returns faithful full
	// replacement content.
	var doc struct {
		Content string `json:"content"`
	}
	_ = callTool(ctx, s, "read_doc", map[string]any{"docId": c.DocID}, &doc)

	dec, err := b.decide(ctx, c, doc.Content)
	if err != nil {
		return fmt.Errorf("decide: %w", err)
	}

	switch dec.Action {
	case "propose":
		return callTool(ctx, s, "propose_suggestion", map[string]any{
			"commentId": c.ID,
			"token":     claim.Token,
			"content":   dec.Content,
			"agent":     b.AgentID,
		}, nil)
	default: // "reply" (and any unrecognized action) → discuss, never edit blindly
		return callTool(ctx, s, "reply_in_thread", map[string]any{
			"commentId": c.ID,
			"token":     claim.Token,
			"body":      dec.Content,
			"agent":     b.AgentID,
		}, nil)
	}
}

// callTool invokes an MCP tool and, when out is non-nil, decodes the tool's
// structured result into it. It surfaces tool-level errors (IsError) as Go
// errors so the caller can react.
func callTool(ctx context.Context, s *mcp.ClientSession, name string, args any, out any) error {
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err
	}
	if res.IsError {
		return fmt.Errorf("tool %s returned error: %s", name, contentText(res))
	}
	if out == nil || res.StructuredContent == nil {
		return nil
	}
	// StructuredContent is a decoded JSON value (typically map[string]any);
	// re-marshal then decode into the caller's typed struct.
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// contentText flattens a tool result's text content for error messages.
func contentText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

// decide asks Anthropic how to respond to one comment, returning a structured
// decision. The system prompt carries the anti-sycophancy guidance from
// AGENTS.md so api mode behaves like the documented agent.
func (b *APIBackend) decide(ctx context.Context, c openComment, docContent string) (decision, error) {
	var thread strings.Builder
	for _, m := range c.Thread {
		fmt.Fprintf(&thread, "- [%s] %s\n", m.AuthorIdentity, m.Body)
	}
	user := fmt.Sprintf(`A human left a comment on a Markdown spec.

Document path: %s
Anchored excerpt (the exact text they flagged):
%q

Thread (their feedback):
%s
Full current document:
---
%s
---

Decide how to respond. A comment is NOT an order: propose an edit ONLY when the
feedback is correct on technical merit; otherwise reply to counter, clarify, or
disagree with evidence from the document. Never resolve/accept/approve.

Respond with ONLY a JSON object, no prose:
{"action":"propose","content":"<the FULL replacement document>"}
or
{"action":"reply","content":"<your thread reply>"}`,
		c.DocPath, c.Excerpt, thread.String(), docContent)

	const system = "You are an unbiased reviewer processing an outbox of comments on a Markdown spec. " +
		"Resist the pull toward agreement: engage on technical merit, disagree when warranted, keep edits " +
		"minimal and faithful, and never invent facts. You propose and discuss only; the human decides."

	text, err := callAnthropic(ctx, b.APIKey, b.Model, system, user)
	if err != nil {
		return decision{}, err
	}
	return parseDecision(text)
}

// parseDecision extracts the JSON decision from the model's reply, tolerating a
// stray code fence or surrounding prose by scanning for the outermost braces.
func parseDecision(text string) (decision, error) {
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return decision{}, fmt.Errorf("no JSON object in model reply: %q", text)
	}
	var d decision
	if err := json.Unmarshal([]byte(text[start:end+1]), &d); err != nil {
		return decision{}, fmt.Errorf("decode decision: %w", err)
	}
	if d.Action != "propose" && d.Action != "reply" {
		return decision{}, fmt.Errorf("unknown action %q", d.Action)
	}
	return d, nil
}

// callAnthropic makes one Anthropic Messages API call and returns the assembled
// text. Kept minimal and dependency-free on purpose — it shows the exact wire
// contract (headers incl. anthropic-version) rather than hiding it behind an SDK.
func callAnthropic(ctx context.Context, apiKey, model, system, user string) (string, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"system":     system,
		"messages": []map[string]any{
			{"role": "user", "content": user},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("anthropic status %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

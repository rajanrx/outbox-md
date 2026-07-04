package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rajanrx/outbox-md/internal/rag"
	"github.com/rajanrx/outbox-md/internal/store"
)

// KBSearchFunc retrieves the top-k KB passages for a question within a project.
// It is a seam (like decisionSink) so the service never constructs a rag.Searcher
// or imports the embedder — buildServer wires it to the shared searcher.
type KBSearchFunc func(project, query string, k int) ([]rag.Result, error)

// KBLLMFunc runs the project's configured LLM as a PLAIN completion: it substitutes
// prompt into the resolved command and returns the model's stdout as the answer.
// It is a seam so the service never shells out or imports exec/rag construction —
// buildServer resolves the project's chair/agent command and supplies the spawn.
type KBLLMFunc func(project, prompt string) (string, error)

// AskKBTopK is how many passages AskKB retrieves to ground an answer.
const AskKBTopK = 6

// ErrKBNotWired is returned by AskKB when the retrieval/LLM seams are not installed
// (a build without the KB, or a test that only calls SetConfig). The API maps it to
// a clear 503 rather than a nil-panic.
var ErrKBNotWired = errors.New("knowledge-base chat is not available on this server")

// SetKBChat installs the retrieval + LLM seams the Ask-the-KB path drives. Unset
// (either nil) ⇒ AskKB returns ErrKBNotWired, so the governance flow is unchanged
// when the KB is not wired.
func (s *Service) SetKBChat(search KBSearchFunc, llm KBLLMFunc) {
	s.kbSearch = search
	s.kbLLM = llm
}

// AskKB answers question against project's knowledge base, grounded in retrieved
// passages, and persists the exchange on sessionID. The order is deliberate:
// retrieve → LLM → persist BOTH turns. Nothing is written until the LLM answers, so
// an LLM/spawn error surfaces cleanly and never leaves a dangling half-turn on the
// session. It returns the persisted assistant message (carrying its sources).
func (s *Service) AskKB(project, sessionID, question string) (store.RagMessage, error) {
	if s.kbSearch == nil || s.kbLLM == nil {
		return store.RagMessage{}, ErrKBNotWired
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return store.RagMessage{}, errors.New("question is required")
	}
	// The session must exist (and its project must match) before we spend an LLM
	// call — a bad id fails fast with a clear error.
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return store.RagMessage{}, err
	}
	// Retrieve first — the grounding context. A search error is fatal (the whole
	// point is a grounded answer); an empty result set is fine (the prompt tells the
	// model to say the KB doesn't cover it).
	passages, err := s.kbSearch(sess.Project, question, AskKBTopK)
	if err != nil {
		return store.RagMessage{}, fmt.Errorf("knowledge-base search failed: %w", err)
	}
	prompt := buildGroundedPrompt(question, passages)

	// The LLM call is the only step that can fail slowly. Do it BEFORE any write so a
	// failure corrupts nothing: no user turn, no assistant turn, session untouched.
	answer, err := s.kbLLM(sess.Project, prompt)
	if err != nil {
		return store.RagMessage{}, fmt.Errorf("knowledge-base answer failed: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = "The model returned an empty answer. Try rephrasing the question."
	}

	// Persist both turns only now that we have an answer. The user turn carries no
	// sources; the assistant turn carries the retrieved passages it was grounded in.
	if _, err := s.store.AppendMessage(store.RagMessage{
		SessionID: sessionID, Role: store.RagRoleUser, Content: question,
	}); err != nil {
		return store.RagMessage{}, err
	}
	assistant, err := s.store.AppendMessage(store.RagMessage{
		SessionID: sessionID, Role: store.RagRoleAssistant, Content: answer, Sources: passages,
	})
	if err != nil {
		return store.RagMessage{}, err
	}
	return assistant, nil
}

// groundedPromptHeader instructs the model to answer strictly from the supplied
// context, cite the doc paths it used, and admit when the context does not cover
// the question. It is deliberately plain Q&A — no outbox governance tools, no
// review loop.
const groundedPromptHeader = `You are answering a question using ONLY the knowledge-base passages provided below. ` +
	`Follow these rules strictly:
- Answer the question using ONLY the information in the CONTEXT. Do not use outside knowledge.
- Cite the document paths you drew from, e.g. "(source: docs/spec.md)".
- If the CONTEXT does not contain the answer, say so plainly — do not invent one.
- Be concise and direct. Format the answer as Markdown.`

// buildGroundedPrompt assembles the grounded completion prompt: the instruction
// header, the retrieved passages (each with its doc path + heading + a bounded
// snippet), and the question. Each passage is bounded (snippet already trimmed by
// the searcher) so the prompt never balloons on a large retrieval.
func buildGroundedPrompt(question string, passages []rag.Result) string {
	var b strings.Builder
	b.WriteString(groundedPromptHeader)
	b.WriteString("\n\n===== CONTEXT =====\n")
	if len(passages) == 0 {
		b.WriteString("(no relevant passages were found in the knowledge base)\n")
	}
	for i, p := range passages {
		heading := p.Heading
		if heading != "" {
			heading = " › " + heading
		}
		fmt.Fprintf(&b, "\n[%d] %s%s\n%s\n", i+1, p.DocPath, heading, p.Snippet)
	}
	b.WriteString("\n===== QUESTION =====\n")
	b.WriteString(question)
	b.WriteString("\n\n===== ANSWER =====\n")
	return b.String()
}

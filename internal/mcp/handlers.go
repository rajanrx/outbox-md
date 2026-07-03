package mcp

import (
	"fmt"
	"sort"
	"time"

	"github.com/rajanrx/outbox-md/internal/anchor"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/rag"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

// RAGSearcher is the read-only retrieval seam the search_knowledge_base tool
// drives. *rag.Searcher satisfies it; it is project-scoped (kb_scope:all is a
// later slice — this slice always scopes to one project). Nil ⇒ retrieval is not
// wired and the tool returns no hits.
type RAGSearcher interface {
	Search(project, query string, k int) ([]rag.Result, error)
	// SearchKind restricts retrieval to one chunk kind — rag.KindDoc (the default)
	// for served documents, rag.KindDecision for durable decision memory (§5.4).
	SearchKind(project, query, kind string, k int) ([]rag.Result, error)
}

type Handlers struct {
	Svc *service.Service
	St  *store.Store
	// RAG is the knowledge-base searcher backing search_knowledge_base. Optional:
	// nil ⇒ the tool is a no-op returning zero hits (retrieval not wired).
	RAG RAGSearcher
}

// KBHit is one search_knowledge_base result: the source doc + heading for
// citation, a snippet preview, the blended relevance score (higher = better), and
// the kind ("doc" | "decision") so an agent can tell a served passage from a
// recalled prior decision (§5.4).
type KBHit struct {
	DocPath string  `json:"docPath"`
	Heading string  `json:"heading"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
	Kind    string  `json:"kind"`
}

// SearchKnowledgeBase runs project-scoped retrieval over the served corpus and
// returns the top-k passages. project selects the corpus; empty defaults to the
// sole served project (the single-folder "" project, or the one registered
// project). It NEVER returns a passage from a doc outside its project's active
// sources whitelist — 3A only indexes served docs, so this is belt-and-suspenders
// over that invariant. k <= 0 falls back to the searcher default.
func (h *Handlers) SearchKnowledgeBase(project, query string, k int) ([]KBHit, error) {
	return h.SearchKnowledgeBaseKind(project, query, rag.KindDoc, k)
}

// SearchKnowledgeBaseKind is SearchKnowledgeBase scoped to a chunk kind (§5.4):
// kind="doc" (the default, and what search_knowledge_base uses when kind is
// omitted) retrieves served documents; kind="decision" restricts to durable
// decision memory — prior council verdicts and accepted changes. A blank/unknown
// kind falls back to "doc". The sources-whitelist filter applies ONLY to doc hits:
// decisions are project-scoped memory, not served files, so they surface even
// under an active whitelist (their DocPath is a real doc for citation but need not
// be currently served).
func (h *Handlers) SearchKnowledgeBaseKind(project, query, kind string, k int) ([]KBHit, error) {
	if h.RAG == nil {
		return []KBHit{}, nil
	}
	if kind != rag.KindDecision {
		kind = rag.KindDoc
	}
	project = h.resolveKBProject(project)
	results, err := h.RAG.SearchKind(project, query, kind, k)
	if err != nil {
		return nil, err
	}
	restricted := h.Svc != nil && h.Svc.SourcesRestricted()
	out := make([]KBHit, 0, len(results))
	for _, r := range results {
		if restricted && r.Kind == rag.KindDoc && !h.Svc.ProjectServes(project, r.DocPath) {
			continue // hidden doc: never leak a chunk (assert the whitelist)
		}
		out = append(out, KBHit{
			DocPath: r.DocPath,
			Heading: r.Heading,
			Snippet: r.Snippet,
			Score:   r.Score,
			Kind:    r.Kind,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// resolveKBProject maps an omitted project to the sole served project so an agent
// spawned per-project (cwd = its root) need not name it. A non-empty project is
// honoured verbatim (an unknown project simply retrieves nothing). With exactly
// one served project its name is used (the single-folder "" project or the one
// registered project); with none registered it stays "" (single-folder mode).
func (h *Handlers) resolveKBProject(project string) string {
	if project != "" || h.Svc == nil {
		return project
	}
	if ps := h.Svc.Projects(); len(ps) == 1 {
		return ps[0].Name
	}
	return project
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

// ClaimComment claims the requested comments and returns the shared claim token
// PLUS the subset actually WON. Under fan-out (N concurrent agents per project)
// two agents can request the same id; the claim is a per-id compare-and-swap, so
// at most one wins and the loser simply sees that id absent from the returned
// set. The agent must process ONLY the won ids and move on from the rest.
func (h *Handlers) ClaimComment(ids []string, agent string) (string, []string, error) {
	for _, id := range ids {
		if !h.served(id) {
			return "", nil, fmt.Errorf("comment %s not found", id)
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
// member's review as a Candidate instead of a single baseline suggestion. round
// is the discussion pass (0 = the independent take, >=1 = a discussion revision).
// It never resolves or writes disk — picking/accepting stay human-only.
func (h *Handlers) SubmitReview(commentID, token string, round int, lens, verdict, rationale, content, agentIdentity string) (domain.Candidate, error) {
	if !h.served(commentID) {
		return domain.Candidate{}, fmt.Errorf("comment %s not found", commentID)
	}
	return h.Svc.SubmitReview(commentID, token, round, lens, verdict, rationale, content, agentIdentity)
}

// SubmitDiscussion records one attributed message on a comment's council
// discussion transcript. It gates on served() like every comment-scoped write so
// an agent can't reach a hidden doc, and is purely additive: it resolves nothing,
// writes no file, and changes no comment status.
func (h *Handlers) SubmitDiscussion(commentID, token string, round int, body string, refs []domain.DiscussionRef, agentIdentity string) (domain.DiscussionMessage, error) {
	if !h.served(commentID) {
		return domain.DiscussionMessage{}, fmt.Errorf("comment %s not found", commentID)
	}
	return h.Svc.SubmitDiscussion(commentID, token, round, body, refs, agentIdentity)
}

// ListCandidates is the council chair's read path: the candidate set, every
// member's independent candidate, and the synthesis if one was recorded. It
// gates on served() like the other reads so it can't reach a hidden doc.
func (h *Handlers) ListCandidates(commentID string) (service.CouncilView, error) {
	if !h.served(commentID) {
		return service.CouncilView{}, fmt.Errorf("comment %s not found", commentID)
	}
	return h.Svc.ListCandidates(commentID)
}

// RecordSynthesis is the council chair's verdict path: it records the chair's
// roll-up as a Synthesis and, when it carries edit content, emits an ordinary
// Suggestion the human then accepts unchanged. Token-authed via the service like
// every write path, so only the claiming council may record. agentIdentity is
// the chair's identity (stored as the synthesis creator).
func (h *Handlers) RecordSynthesis(commentID, token, content, dissent string, agreementScore float64, confidence int, agentIdentity string) (domain.Synthesis, error) {
	if !h.served(commentID) {
		return domain.Synthesis{}, fmt.Errorf("comment %s not found", commentID)
	}
	return h.Svc.RecordSynthesis(commentID, token, dissent, content, agentIdentity, agreementScore, confidence)
}

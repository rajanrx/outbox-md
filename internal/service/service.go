package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/rajanrx/outbox-md/internal/anchor"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/registry"
	"github.com/rajanrx/outbox-md/internal/store"
	"github.com/rajanrx/outbox-md/internal/webhook"
)

// ErrNoCandidateSet means a comment has no candidate set yet — a "not found"
// signal distinct from a genuine store error, so callers (e.g. the API) can
// map it to 404 rather than 500.
var ErrNoCandidateSet = errors.New("no candidate set for comment")

// DefaultProcessingTTL is how long a processing hint lives when the caller does
// not specify one. It is bounded by design — long enough to cover a typical
// agent run, short enough that a dead agent's hint self-expires; a live agent
// re-marks (heartbeats) to extend it.
const DefaultProcessingTTL = 180 * time.Second

// ReceivedTTL is the lifetime of the instant "received" ack a runner records the
// moment a webhook lands — before any agent claims the comment. It is shorter
// than DefaultProcessingTTL: it only has to bridge the gap until the claiming
// agent calls mark_processing (which extends the hint to the full run window),
// so if the agent never shows up the badge self-clears quickly.
const ReceivedTTL = 30 * time.Second

type Service struct {
	store *store.Store
	// writeFile persists a document's content to disk. It receives the document's
	// project so the caller can route the write to the correct project folder; the
	// empty project is the single-folder mode.
	writeFile func(project, path, content string) error
	cfg       config.Config
	notify    webhook.Notifier
	projects  []registry.Project
	// version is the CLI/server version string surfaced at /api/config so the UI
	// can show a build badge. "dev" for local builds; set once at startup.
	version string
	// projectSources, when non-nil, is the per-project runtime sources map built
	// by the server at startup (single-folder mode is a single "" entry carrying
	// the real cfg). It drives the read guards so each project's Sources whitelist
	// is enforced against its OWN docs. When nil — e.g. tests that only call
	// SetConfig — the guards fall back to the single global cfg, preserving the
	// pre-multi-project single-whitelist semantics.
	projectSources config.ProjectSources
}

func New(st *store.Store, writeFile func(project, path, content string) error) *Service {
	return &Service{store: st, writeFile: writeFile, cfg: config.Defaults(), notify: webhook.Nop{}, version: "dev"}
}

// SetVersion records the build version surfaced at /api/config (set once at
// startup from the injected main.version).
func (s *Service) SetVersion(v string) { s.version = v }

// Version returns the build version (defaults to "dev").
func (s *Service) Version() string { return s.version }

// SetConfig replaces the effective configuration (called once at startup with
// the loaded outbox.yaml).
func (s *Service) SetConfig(cfg config.Config) { s.cfg = cfg }

// SetProjects records the registered projects being served (for the projects
// API). The single-folder mode passes a single entry with an empty name.
func (s *Service) SetProjects(p []registry.Project) { s.projects = p }

// Projects returns the projects being served (read-only view for the API).
func (s *Service) Projects() []registry.Project { return s.projects }

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
		payload.Project = doc.Project
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
		Project: doc.Project,
		TS:      time.Now().UTC().Format(time.RFC3339),
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

// SetProjectSources installs the per-project runtime sources map (built once at
// startup). Leaving it unset keeps the guards on the single global cfg.
func (s *Service) SetProjectSources(m config.ProjectSources) { s.projectSources = m }

// ProjectServes reports whether docPath in the given project is inside that
// project's active sources whitelist. With a per-project map it resolves the
// project's own cfg (an unknown project → hidden, so orphaned docs stay
// hidden). Without one (map nil) it falls back to the single global cfg, so
// existing single-config callers and tests behave exactly as before.
func (s *Service) ProjectServes(project, docPath string) bool {
	if s.projectSources != nil {
		return s.projectSources.Serves(project, docPath)
	}
	return s.cfg.Serves(docPath)
}

// SourcesRestricted reports whether the sources guards must run. It is false
// only for single-folder mode with no whitelist — the zero-extra-lookup fast
// path where every doc is served and the guard would be a pure no-op.
func (s *Service) SourcesRestricted() bool {
	if s.projectSources != nil {
		return s.projectSources.Restricted()
	}
	return len(s.cfg.Sources) > 0
}

// SyncFile reconciles the on-disk .md file keyed (project, path) into the store.
// It is the write half of the live filesystem watcher: it creates the document
// when absent and appends a new version when the content differs from the
// current one. When the content is byte-identical to the current version it is a
// no-op returning changed=false — this is what stops the server's OWN
// accept/reapprove disk writes (atomicWrite → rename, which the watcher sees)
// from looping back into a spurious new version. It never writes disk (the file
// on disk is the source of truth here) and never fires webhook events, so the
// watcher path can never re-trigger the auto-reply engine. changed reports
// whether the store actually changed (new doc or new version), so the caller
// only broadcasts docs.changed when there is something to see.
func (s *Service) SyncFile(project, path, content string) (bool, error) {
	doc, ok, err := s.store.GetDocumentByPath(project, path)
	if err != nil {
		return false, err
	}
	if !ok {
		if _, _, err := s.store.CreateDocumentInProject(project, path, content, "watch"); err != nil {
			return false, err
		}
		return true, nil
	}
	cur, err := s.store.GetVersion(doc.CurrentVersionID)
	if err != nil {
		return false, err
	}
	if cur.Content == content {
		return false, nil
	}
	if _, err := s.store.AddVersion(doc.ID, content, "watch"); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveFile drops the document keyed (project, path) from the store when its
// .md file is deleted or renamed away on disk. It returns removed=false (no
// error) when no such document exists, so a delete event for a file the store
// never knew about is a harmless no-op.
func (s *Service) RemoveFile(project, path string) (bool, error) {
	doc, ok, err := s.store.GetDocumentByPath(project, path)
	if err != nil || !ok {
		return false, err
	}
	if err := s.store.DeleteDocument(doc.ID); err != nil {
		return false, err
	}
	return true, nil
}

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

// PendingCommentCount reports how many comments in project still need agent
// attention right now — the 'open' comments plus any 'claimed' comment whose
// claim has been abandoned (a stale claim). It is the drain signal for the
// in-process auto-reply engine: after a run it re-checks this count and, while a
// run keeps making progress (the count keeps dropping) and work remains,
// schedules another run so a burst that one run only partly cleared is drained
// out. The empty project name counts single-folder-mode comments.
func (s *Service) PendingCommentCount(project string) (int, error) {
	comments, err := s.store.ListOpenComments(time.Now().UTC())
	if err != nil {
		return 0, err
	}
	restricted := s.SourcesRestricted()
	n := 0
	for _, c := range comments {
		doc, err := s.store.GetDocument(c.DocID)
		if err != nil || doc.Project != project {
			continue
		}
		// Same sources whitelist as the read/write/council paths: a comment on a doc
		// hidden by narrowed sources must NOT count as pending, else it keeps
		// triggering/draining runs for work the council will never (rightly) touch.
		if restricted && !s.ProjectServes(doc.Project, doc.Path) {
			continue
		}
		n++
	}
	return n, nil
}

// CouncilComment is a project's open comment plus the review context the council
// orchestration hands its members: the doc to read, the flagged excerpt, and the
// human's thread. A freshly-claimed comment is hidden from list_open_comments and
// read_doc needs the docId, so members can't fetch these themselves — the engine
// embeds them in the member prompt.
type CouncilComment struct {
	CommentID string
	DocID     string
	DocPath   string
	Excerpt   string
	Thread    []domain.ThreadMessage
}

// OpenCouncilComments returns the open (+ recovered stale-claimed) comments a
// council pass drives for a project, each with review context. It applies the
// SAME sources whitelist as the HTTP/MCP read/write paths (SourcesRestricted &&
// !ProjectServes) — so the council never claims, heartbeats, or reviews a comment
// on a doc hidden by narrowed sources. Excerpt/thread are best-effort (a failed
// lookup just leaves the field empty; read_doc still gives the member the doc).
func (s *Service) OpenCouncilComments(project string) ([]CouncilComment, error) {
	comments, err := s.store.ListOpenComments(time.Now().UTC())
	if err != nil {
		return nil, err
	}
	restricted := s.SourcesRestricted()
	out := make([]CouncilComment, 0, len(comments))
	for _, c := range comments {
		doc, err := s.store.GetDocument(c.DocID)
		if err != nil || doc.Project != project {
			continue
		}
		if restricted && !s.ProjectServes(doc.Project, doc.Path) {
			continue
		}
		cc := CouncilComment{CommentID: c.ID, DocID: c.DocID, DocPath: doc.Path}
		if ver, err := s.store.GetVersion(c.AgainstVersionID); err == nil {
			cc.Excerpt = anchor.Excerpt(ver.Content, c.Anchor.Start, c.Anchor.End)
		}
		if th, err := s.store.ListThread(c.ID); err == nil {
			cc.Thread = th
		}
		out = append(out, cc)
	}
	return out, nil
}

// Claim atomically claims a batch of comments for one agent run and returns the
// shared claim token PLUS the subset of ids actually WON. With a bounded pool of
// N agents per project (fan-out), two agents can race to claim the same comment;
// each id is claimed via a compare-and-swap (store.ClaimCommentCAS) so at most
// one wins. An id lost to a concurrent claim — or no longer claimable — is simply
// omitted from the returned set (a skip, not a fatal error), so the caller
// processes only what it actually holds and moves on from the rest. A genuine
// store error aborts the whole claim. The staleness/now semantics match
// ListOpenComments, so the claimable set equals the set the agent was offered.
func (s *Service) Claim(commentIDs []string, agent string) (string, []string, error) {
	if len(commentIDs) > s.cfg.Agent.BatchSize {
		return "", nil, fmt.Errorf("batch size exceeded: at most %d comments per claim", s.cfg.Agent.BatchSize)
	}
	token := domain.NewID()
	now := time.Now().UTC()
	claimed := make([]string, 0, len(commentIDs))
	for _, id := range commentIDs {
		won, err := s.store.ClaimCommentCAS(id, token, now, store.StaleClaimGrace)
		if err != nil {
			return "", nil, err
		}
		if won {
			claimed = append(claimed, id)
		}
	}
	return token, claimed, nil
}

// MarkProcessing records an ephemeral, self-expiring hint that the claiming
// agent is actively working commentID, so the human sees an "AI processing…"
// indicator live. It requires a valid claim token (only the claiming agent may
// mark), writes no file, and changes no status. Re-calling extends the deadline
// (a heartbeat for long runs). A ttl <= 0 falls back to DefaultProcessingTTL. It
// fires a browser-only comment.processing SSE event (absent from the webhook's
// default set, so the runner is never re-triggered) and returns the new deadline.
func (s *Service) MarkProcessing(commentID, token string, ttl time.Duration) (time.Time, error) {
	c, err := s.requireToken(commentID, token)
	if err != nil {
		return time.Time{}, err
	}
	if ttl <= 0 {
		ttl = DefaultProcessingTTL
	}
	until := time.Now().UTC().Add(ttl)
	if err := s.store.SetProcessingUntil(commentID, &until); err != nil {
		return time.Time{}, err
	}
	s.fireCommentEvent(webhook.EventCommentProcessing, c)
	return until, nil
}

// MarkReceived records the runner's instant "received" ack: it sets the
// ephemeral processing hint on commentID for ReceivedTTL so the human sees the
// "AI processing…" badge within ~1s of a webhook — and even if the agent dies
// before it ever claims. Unlike MarkProcessing it is deliberately UNTOKENED: at
// this point no agent has claimed the comment yet, so there is no claim token to
// present. It loads the comment (to build the event payload and to reject an
// unknown id), sets ProcessingUntil = now+ReceivedTTL, fires the browser-only
// comment.processing SSE event (absent from the webhook default set, so this ack
// never re-triggers the runner), and returns the deadline. The tokened
// MarkProcessing then extends this once the agent is actually working.
func (s *Service) MarkReceived(commentID string) (time.Time, error) {
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return time.Time{}, err
	}
	until := time.Now().UTC().Add(ReceivedTTL)
	if err := s.store.SetProcessingUntil(commentID, &until); err != nil {
		return time.Time{}, err
	}
	s.fireCommentEvent(webhook.EventCommentProcessing, c)
	return until, nil
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

// checkNotStale returns a clear, versioned error when comment c was read/claimed
// against a version older than its document's current version — i.e. the doc
// changed since the agent read it, so any suggestion built now is stale. It is
// the earliest point to reject (at propose time), so the agent never wastes a
// proposal on outdated content. A best-effort ordinal lookup enriches the
// message ("v3→v5"); if a lookup fails the message still names the version ids.
func (s *Service) checkNotStale(c domain.Comment) error {
	doc, err := s.store.GetDocument(c.DocID)
	if err != nil {
		return err
	}
	if c.AgainstVersionID == doc.CurrentVersionID {
		return nil
	}
	from, to := c.AgainstVersionID, doc.CurrentVersionID
	if v, err := s.store.GetVersion(c.AgainstVersionID); err == nil {
		from = fmt.Sprintf("v%d", v.Ordinal)
	}
	if v, err := s.store.GetVersion(doc.CurrentVersionID); err == nil {
		to = fmt.Sprintf("v%d", v.Ordinal)
	}
	return fmt.Errorf("document changed since you read it (%s→%s); re-read the document and re-propose against its current content", from, to)
}

func (s *Service) Propose(commentID, token, content, agent string) (domain.Suggestion, error) {
	c, err := s.requireToken(commentID, token)
	if err != nil {
		return domain.Suggestion{}, err
	}
	// Pre-post staleness gate: reject the propose if the document has moved on
	// since the agent read/claimed it. The comment's AgainstVersionID is the
	// version the agent saw (Accept's rebase keeps sibling comments in lockstep
	// with the current pointer; a watcher-imported on-disk edit adds a version but
	// does NOT rebase — the genuine stale case). If it no longer equals the doc's
	// current version, the agent is proposing against outdated content, which
	// Accept would later reject anyway. Fail EARLY and loudly so the agent
	// re-reads and re-proposes against current content — never silently store a
	// suggestion built on stale text. This matters more under fan-out + live
	// editing, where the doc can change mid-run.
	if err := s.checkNotStale(c); err != nil {
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
	// The agent has finished this comment — clear the processing hint before firing
	// so the browser's refresh reads no stale deadline.
	_ = s.store.SetProcessingUntil(commentID, nil)
	// Push the agent's proposal to the browser (SSE) so the UI reflects it live.
	// suggestion.proposed is NOT in the webhook's default Events, so the HTTP
	// runner is not re-triggered by the agent's own action — only the browser is.
	s.fireCommentEvent(webhook.EventSuggestionProposed, c)
	return sg, nil
}

// --- AI Council (roadmap §3) ---------------------------------------------
//
// The council path is ADDED alongside Propose, not on top of it. The server
// stores candidates and records the decision; it never calls a model, holds a
// key, or runs quorum/tiebreak/chair logic — that lives in the external webhook
// runner. The human-only pick/synthesis emit an ordinary Suggestion so the
// accept-flow is exactly today's.

// CouncilView is the read model for a comment's council: the set, its candidates
// in submission order, and the synthesis (if recorded).
type CouncilView struct {
	Set        domain.CandidateSet `json:"set"`
	Candidates []domain.Candidate  `json:"candidates"`
	Synthesis  *domain.Synthesis   `json:"synthesis"`
}

// SubmitReview records one council member's review as a Candidate. It validates
// the claim token (members share the comment's token, distinguished by
// agentIdentity), lazily creates the comment's CandidateSet, and enforces
// content required iff verdict == edit. It never writes disk, never resolves,
// and never changes the comment's status — candidates accumulate while the set
// stays gathering.
func (s *Service) SubmitReview(commentID, token, lens, verdict, rationale, content, agentIdentity string) (domain.Candidate, error) {
	if _, err := s.requireToken(commentID, token); err != nil {
		return domain.Candidate{}, err
	}
	switch lens {
	case domain.LensCorrectness, domain.LensCompleteness, domain.LensAmbiguity,
		domain.LensRisk, domain.LensSimplicity, domain.LensSkeptic:
	default:
		return domain.Candidate{}, fmt.Errorf("invalid lens %q", lens)
	}
	switch verdict {
	case domain.VerdictEdit:
		if content == "" {
			return domain.Candidate{}, errors.New("content is required when verdict is edit")
		}
	case domain.VerdictReply, domain.VerdictRejectComment:
		if content != "" {
			return domain.Candidate{}, fmt.Errorf("content must be empty when verdict is %q", verdict)
		}
	default:
		return domain.Candidate{}, fmt.Errorf("invalid verdict %q", verdict)
	}
	set, err := s.store.GetOrCreateCandidateSet(commentID)
	if err != nil {
		return domain.Candidate{}, err
	}
	return s.store.AddCandidate(domain.Candidate{
		CandidateSetID: set.ID, Lens: lens, Verdict: verdict,
		Rationale: rationale, Content: content, AgentIdentity: agentIdentity,
	})
}

// ListCandidates returns the council view for a comment: the set, its candidates
// in order, and the synthesis if any. Returns an error if no set exists yet.
func (s *Service) ListCandidates(commentID string) (CouncilView, error) {
	set, ok, err := s.store.GetCandidateSetByComment(commentID)
	if err != nil {
		return CouncilView{}, err
	}
	if !ok {
		return CouncilView{}, ErrNoCandidateSet
	}
	cands, err := s.store.ListCandidatesByComment(commentID)
	if err != nil {
		return CouncilView{}, err
	}
	view := CouncilView{Set: set, Candidates: cands}
	if syn, ok, err := s.store.GetSynthesisByComment(commentID); err != nil {
		return CouncilView{}, err
	} else if ok {
		view.Synthesis = &syn
	}
	return view, nil
}

// RecordSynthesis records the chair's roll-up of a candidate set and, when it
// carries an edit, emits an ordinary Suggestion via the existing path so the
// human's accept-flow is unchanged. The chair is the external runner; only the
// claiming council may record, so it validates the shared claim token like every
// other write path. confidence (0..100) is the chair's confidence in the verdict.
func (s *Service) RecordSynthesis(commentID, token, dissent, content, createdBy string, agreementScore float64, confidence int) (domain.Synthesis, error) {
	c, err := s.requireToken(commentID, token)
	if err != nil {
		return domain.Synthesis{}, err
	}
	set, err := s.store.GetOrCreateCandidateSet(commentID)
	if err != nil {
		return domain.Synthesis{}, err
	}
	// An edit verdict must reject a stale doc BEFORE claiming the synthesis slot,
	// so a stale edit never leaves the set synthesized-but-suggestionless.
	if content != "" {
		if err := s.checkNotStale(c); err != nil {
			return domain.Synthesis{}, err
		}
	}
	// Single-shot, concurrency-safe: atomically claim gathering→synthesized. Two
	// OVERLAPPING chair calls race on this one UPDATE (writes serialized by
	// SetMaxOpenConns(1)); exactly one wins, and the loser is rejected BEFORE
	// emitting — so a comment never gets two syntheses or two suggestions (a
	// sequential retry loses the same way, since the set has left 'gathering').
	if won, err := s.store.TryClaimSynthesis(set.ID); err != nil {
		return domain.Synthesis{}, err
	} else if !won {
		return domain.Synthesis{}, errors.New("candidate set already synthesized or decided")
	}
	suggestionID := ""
	if content != "" {
		// Edit verdict → an accept-eligible suggestion (emitSuggestion also runs the
		// stale guard; the doc was just checked, so it passes) and marks the comment
		// addressed.
		sg, err := s.emitSuggestion(c, content, createdBy)
		if err != nil {
			return domain.Synthesis{}, err
		}
		suggestionID = sg.ID
	} else {
		// No-edit verdict (reply/reject/no-consensus): the council concluded
		// WITHOUT a change. Post the verdict as a chair reply and move the comment
		// out of 'claimed' (→ replied) so the human sees a terminal outcome and
		// stale-claim recovery can't re-loop it. comment.updated (not .replied) so
		// the runner is never re-triggered by the council's own reply.
		body := fmt.Sprintf("Council verdict: no edit (agreement %.0f%%, confidence %d%%).", agreementScore*100, confidence)
		if dissent != "" {
			body += " Dissent: " + dissent
		}
		if _, err := s.store.AddThreadMessage(domain.ThreadMessage{
			CommentID: commentID, AuthorIdentity: createdBy, Body: body,
		}); err != nil {
			return domain.Synthesis{}, err
		}
		if err := s.store.UpdateCommentStatus(commentID, domain.CommentReplied, c.ClaimToken); err != nil {
			return domain.Synthesis{}, err
		}
		_ = s.store.SetProcessingUntil(commentID, nil)
		s.fireCommentEvent(webhook.EventCommentUpdated, c)
	}
	syn, err := s.store.RecordSynthesis(domain.Synthesis{
		CandidateSetID: set.ID, AgreementScore: agreementScore, Confidence: confidence,
		Dissent: dissent, SuggestionID: suggestionID, CreatedBy: createdBy,
	})
	if err != nil {
		return domain.Synthesis{}, err
	}
	// State is already 'synthesized' — set atomically by TryClaimSynthesis above.
	return syn, nil
}

// PickCandidate is the human decision point: the human chooses a specific
// candidate over the synthesis. It is HUMAN-ONLY — actor is server-set by the
// caller (never taken from the request body) and must be the local human, like
// resolve/approve; there is no MCP tool for it. It marks the chosen candidate,
// flips the set to decided, and — if the candidate is an edit — emits the
// accept-eligible Suggestion the human then accepts through the unchanged accept
// path. It does NOT auto-accept.
func (s *Service) PickCandidate(commentID, candidateID, actor string) (domain.Candidate, error) {
	if actor != LocalHuman {
		return domain.Candidate{}, errors.New("only the local human may pick a candidate")
	}
	c, err := s.store.GetComment(commentID)
	if err != nil {
		return domain.Candidate{}, err
	}
	set, ok, err := s.store.GetCandidateSetByComment(commentID)
	if err != nil {
		return domain.Candidate{}, err
	}
	if !ok {
		return domain.Candidate{}, ErrNoCandidateSet
	}
	// Terminal guard: a set is decided exactly once. A second pick would
	// double-mark chosen and emit a second orphaned Suggestion, so reject it
	// before any write.
	if set.State == domain.CandidateSetDecided {
		return domain.Candidate{}, errors.New("candidate set already decided")
	}
	cand, err := s.store.GetCandidate(candidateID)
	if err != nil {
		return domain.Candidate{}, err
	}
	// Guard: the candidate must belong to this comment's set.
	if cand.CandidateSetID != set.ID {
		return domain.Candidate{}, errors.New("candidate does not belong to this comment")
	}
	// Reject a stale EDIT pick BEFORE mutating state, so it can't leave the set
	// decided-but-suggestionless (emitSuggestion guards too, but that fires after
	// the MarkCandidateChosen/SetState writes below).
	if cand.Verdict == domain.VerdictEdit {
		if err := s.checkNotStale(c); err != nil {
			return domain.Candidate{}, err
		}
	}
	if err := s.store.MarkCandidateChosen(cand.ID); err != nil {
		return domain.Candidate{}, err
	}
	if err := s.store.SetCandidateSetState(set.ID, domain.CandidateSetDecided); err != nil {
		return domain.Candidate{}, err
	}
	cand.Chosen = true
	if cand.Verdict == domain.VerdictEdit {
		if _, err := s.emitSuggestion(c, cand.Content, cand.AgentIdentity); err != nil {
			return domain.Candidate{}, err
		}
	}
	return cand, nil
}

// emitSuggestion creates a proposed Suggestion against the comment's version and
// marks the comment addressed — the same accept-eligible shape Propose produces,
// so the human accepts a council outcome exactly like a single-agent one. It
// does NOT write disk or accept anything.
func (s *Service) emitSuggestion(c domain.Comment, content, createdBy string) (domain.Suggestion, error) {
	// Same stale-version guard as Propose: a council edit must not become an
	// accept-eligible suggestion built against a version the doc has moved past
	// (another accept, or a watcher-imported on-disk edit). Without this, council
	// mode reopens exactly the stale-suggestion hole Propose closes.
	if err := s.checkNotStale(c); err != nil {
		return domain.Suggestion{}, err
	}
	sg, err := s.store.CreateSuggestion(domain.Suggestion{
		CommentID: c.ID, AgainstVersionID: c.AgainstVersionID,
		ProposedContent: content, State: domain.SuggestionProposed, CreatedBy: createdBy,
	})
	if err != nil {
		return domain.Suggestion{}, err
	}
	if err := s.store.UpdateCommentStatus(c.ID, domain.CommentAddressed, c.ClaimToken); err != nil {
		return domain.Suggestion{}, err
	}
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
	if err := s.store.UpdateCommentStatus(commentID, domain.CommentReplied, c.ClaimToken); err != nil {
		return err
	}
	// The agent has finished this comment — clear the processing hint before firing
	// so the browser's refresh reads no stale deadline.
	_ = s.store.SetProcessingUntil(commentID, nil)
	// Push the agent's reply to the browser (SSE) so the UI reflects it live.
	// comment.updated is NOT in the webhook's default Events, so the HTTP runner
	// is not re-triggered by the agent's own reply — only the browser is.
	s.fireCommentEvent(webhook.EventCommentUpdated, c)
	return nil
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
	// A resolved comment is done — clear any lingering processing hint.
	_ = s.store.SetProcessingUntil(commentID, nil)
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
			return s.writeFile(doc.Project, doc.Path, v.Content)
		}
		newVer, err = s.store.AddVersionTx(doc.ID, oldVer.ID, sg.ProposedContent, sg.CreatedBy, writeFn)
	}
	if err != nil {
		if wrote {
			_ = s.writeFile(doc.Project, doc.Path, oldVer.Content)
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
		return s.writeFile(doc.Project, doc.Path, ver.Content)
	})
	if err != nil {
		if wrote {
			_ = s.writeFile(doc.Project, doc.Path, oldBaseline.Content)
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

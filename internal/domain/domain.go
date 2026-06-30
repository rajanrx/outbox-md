package domain

type Anchor struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type DocumentStatus string

const (
	DocDraft    DocumentStatus = "draft"
	DocApproved DocumentStatus = "approved"
	DocAmending DocumentStatus = "amending"
)

type Document struct {
	ID                string         `json:"id"`
	Path              string         `json:"path"`
	CurrentVersionID  string         `json:"currentVersionId"`
	Status            DocumentStatus `json:"status"`
	ApprovedVersionID string         `json:"approvedVersionId"`
}

type Approval struct {
	ID         string `json:"id"`
	DocID      string `json:"docId"`
	VersionID  string `json:"versionId"`
	ApprovedBy string `json:"approvedBy"`
	Note       string `json:"note"`
	CreatedAt  string `json:"createdAt"`
}

type Version struct {
	ID        string `json:"id"`
	DocID     string `json:"docId"`
	Ordinal   int    `json:"ordinal"`
	Content   string `json:"content"`
	CreatedBy string `json:"createdBy"`
}

type CommentStatus string

const (
	CommentOpen      CommentStatus = "open"
	CommentClaimed   CommentStatus = "claimed"
	CommentAddressed CommentStatus = "addressed"
	CommentReplied   CommentStatus = "replied"
	CommentResolved  CommentStatus = "resolved"
	CommentDetached  CommentStatus = "detached"
)

type Comment struct {
	ID               string        `json:"id"`
	DocID            string        `json:"docId"`
	AgainstVersionID string        `json:"againstVersionId"`
	Anchor           Anchor        `json:"anchor"`
	AuthorIdentity   string        `json:"authorIdentity"`
	Owner            string        `json:"owner"`
	Status           CommentStatus `json:"status"`
	PostApproval     bool          `json:"postApproval"`
	ClaimToken       string        `json:"-"`
}

type SuggestionState string

const (
	SuggestionProposed SuggestionState = "proposed"
	SuggestionAccepted SuggestionState = "accepted"
	SuggestionRejected SuggestionState = "rejected"
)

type Suggestion struct {
	ID               string          `json:"id"`
	CommentID        string          `json:"commentId"`
	AgainstVersionID string          `json:"againstVersionId"`
	ProposedContent  string          `json:"proposedContent"`
	State            SuggestionState `json:"state"`
	CreatedBy        string          `json:"createdBy"`
}

type ThreadMessage struct {
	ID             string `json:"id"`
	CommentID      string `json:"commentId"`
	AuthorIdentity string `json:"authorIdentity"`
	Body           string `json:"body"`
}

// AI Council (roadmap §3). A council fans out N independent, blind reviews per
// comment — each a Candidate carrying a lens — plus a Synthesis. The set, its
// candidates, and the synthesis hang off the comment additively; the synthesis
// (or a human pick of an edit) emits an ordinary Suggestion so the human's
// accept-flow is unchanged.

type CandidateSetState string

const (
	CandidateSetGathering   CandidateSetState = "gathering"   // candidates still arriving
	CandidateSetSynthesized CandidateSetState = "synthesized" // chair recorded a synthesis
	CandidateSetDecided     CandidateSetState = "decided"     // human picked a candidate
)

// CandidateSet is the per-comment council, created lazily on the first review.
type CandidateSet struct {
	ID        string            `json:"id"`
	CommentID string            `json:"commentId"`
	State     CandidateSetState `json:"state"`
	Quorum    int               `json:"quorum"` // expected member count, echoed from runner config (0 = unknown)
}

// Lenses a council member can carry (assignable per member); skeptic is the
// dedicated red-team stance.
const (
	LensCorrectness  = "correctness"
	LensCompleteness = "completeness"
	LensAmbiguity    = "ambiguity"
	LensRisk         = "risk"
	LensSimplicity   = "simplicity"
	LensSkeptic      = "skeptic"
)

// A member's verdict on the comment. Content is required iff verdict == edit.
const (
	VerdictEdit          = "edit"
	VerdictReply         = "reply"
	VerdictRejectComment = "reject_comment"
)

// Candidate is one council member's independent review of a comment.
type Candidate struct {
	ID             string `json:"id"`
	CandidateSetID string `json:"candidateSetId"`
	Lens           string `json:"lens"`
	Verdict        string `json:"verdict"`
	Rationale      string `json:"rationale"`
	Content        string `json:"content"` // full replacement IF verdict == edit (else empty)
	AgentIdentity  string `json:"agentIdentity"`
	Chosen         bool   `json:"chosen"` // set by the human-only pick
}

// Synthesis is the chair's roll-up of a candidate set. It only proposes: the
// SuggestionID points at the ordinary Suggestion offered to the human (if any).
type Synthesis struct {
	ID             string  `json:"id"`
	CandidateSetID string  `json:"candidateSetId"`
	AgreementScore float64 `json:"agreementScore"` // 0..1
	Dissent        string  `json:"dissent"`        // the preserved minority/skeptic position
	SuggestionID   string  `json:"suggestionId"`
	CreatedBy      string  `json:"createdBy"`
}

type LogEntry struct {
	Time       string `json:"time"`
	Kind       string `json:"kind"` // created | comment | candidate | synthesis | proposal | edit | approval
	Actor      string `json:"actor"`
	Detail     string `json:"detail"`     // comment excerpt OR approval note OR ""
	Version    int    `json:"version"`    // version ordinal for created/edit/approval; 0 otherwise
	ReApproval bool   `json:"reApproval"` // approval after the first (amendment sign-off)
}

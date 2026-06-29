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

type LogEntry struct {
	Time       string `json:"time"`
	Kind       string `json:"kind"`       // created | comment | proposal | edit | approval
	Actor      string `json:"actor"`
	Detail     string `json:"detail"`     // comment excerpt OR approval note OR ""
	Version    int    `json:"version"`    // version ordinal for created/edit/approval; 0 otherwise
	ReApproval bool   `json:"reApproval"` // approval after the first (amendment sign-off)
}

package domain

type Anchor struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Document struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	CurrentVersionID string `json:"currentVersionId"`
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

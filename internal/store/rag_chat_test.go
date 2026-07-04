package store

import (
	"testing"

	"github.com/rajanrx/outbox-md/internal/rag"
)

func TestRagChatSessionAndMessageRoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sess, err := s.CreateSession("proj-a", "First chat")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.CreatedAt == "" {
		t.Fatalf("session id/createdAt not populated: %+v", sess)
	}

	// A user turn (no sources) then an assistant turn (with sources).
	if _, err := s.AppendMessage(RagMessage{SessionID: sess.ID, Role: RagRoleUser, Content: "what is X?"}); err != nil {
		t.Fatal(err)
	}
	srcs := []rag.Result{
		{DocPath: "docs/x.md", Heading: "X", Snippet: "X is a thing", Score: 0.9, Source: "hybrid", Kind: "doc"},
		{DocPath: "docs/y.md", Heading: "Y", Snippet: "Y relates to X", Score: 0.5, Source: "keyword", Kind: "doc"},
	}
	if _, err := s.AppendMessage(RagMessage{SessionID: sess.ID, Role: RagRoleAssistant, Content: "X is a thing (source: docs/x.md)", Sources: srcs}); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	// Order: user turn first, assistant second.
	if msgs[0].Role != RagRoleUser || msgs[1].Role != RagRoleAssistant {
		t.Fatalf("message order wrong: %s, %s", msgs[0].Role, msgs[1].Role)
	}
	// User turn has empty (non-nil) sources.
	if msgs[0].Sources == nil || len(msgs[0].Sources) != 0 {
		t.Fatalf("user sources = %+v, want empty non-nil", msgs[0].Sources)
	}
	// Assistant sources round-trip fully.
	if len(msgs[1].Sources) != 2 {
		t.Fatalf("assistant sources = %d, want 2", len(msgs[1].Sources))
	}
	got := msgs[1].Sources[0]
	if got.DocPath != "docs/x.md" || got.Heading != "X" || got.Snippet != "X is a thing" || got.Score != 0.9 || got.Source != "hybrid" || got.Kind != "doc" {
		t.Fatalf("sources_json did not round-trip: %+v", got)
	}
}

func TestRagChatListSessionsByProjectAndRename(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	if _, err := s.CreateSession("proj-a", "A1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession("proj-a", "A2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession("proj-b", "B1"); err != nil {
		t.Fatal(err)
	}

	a, err := s.ListSessions("proj-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 {
		t.Fatalf("proj-a sessions = %d, want 2", len(a))
	}
	b, _ := s.ListSessions("proj-b")
	if len(b) != 1 {
		t.Fatalf("proj-b sessions = %d, want 1", len(b))
	}

	// Rename an existing session; an unknown id is a not-found.
	if err := s.RenameSession(b[0].ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSession(b[0].ID)
	if got.Title != "renamed" {
		t.Fatalf("title = %q, want renamed", got.Title)
	}
	if err := s.RenameSession("no-such-id", "x"); err == nil {
		t.Fatal("rename of unknown session should error")
	}
}

func TestRagChatDeleteSessionCascadesMessages(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()

	sess, _ := s.CreateSession("proj-a", "to delete")
	if _, err := s.AppendMessage(RagMessage{SessionID: sess.ID, Role: RagRoleUser, Content: "q"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(RagMessage{SessionID: sess.ID, Role: RagRoleAssistant, Content: "a"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}

	// Session gone.
	if sessions, _ := s.ListSessions("proj-a"); len(sessions) != 0 {
		t.Fatalf("sessions after delete = %d, want 0", len(sessions))
	}
	// Messages cascaded (FKs are not enforced, so DeleteSession must delete them).
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM rag_messages WHERE session_id=?`, sess.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("orphaned messages after delete = %d, want 0", n)
	}
}

// TestAppendExchangeIsAtomic (PR #100 P2): a failed assistant insert must NOT commit
// a dangling user turn — AskKB persists a full exchange or nothing.
func TestAppendExchangeIsAtomic(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	sess, _ := s.CreateSession("p", "t")

	// Happy path: both turns land; the assistant comes back with its timestamp.
	a, err := s.AppendExchange(
		RagMessage{SessionID: sess.ID, Role: RagRoleUser, Content: "q"},
		RagMessage{SessionID: sess.ID, Role: RagRoleAssistant, Content: "a"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if a.Content != "a" || a.CreatedAt == "" {
		t.Fatalf("assistant not returned intact: %+v", a)
	}
	if m, _ := s.ListMessages(sess.ID); len(m) != 2 {
		t.Fatalf("happy exchange = %d messages, want 2", len(m))
	}

	// Force the assistant insert to fail (duplicate PK) → the user turn must roll back.
	if _, err := s.AppendExchange(
		RagMessage{ID: "dup", SessionID: sess.ID, Role: RagRoleUser, Content: "q2"},
		RagMessage{ID: "dup", SessionID: sess.ID, Role: RagRoleAssistant, Content: "a2"},
	); err == nil {
		t.Fatal("duplicate-id exchange should fail")
	}
	if m, _ := s.ListMessages(sess.ID); len(m) != 2 {
		t.Fatalf("failed exchange left %d messages, want 2 (rolled back — only the first pair)", len(m))
	}
}

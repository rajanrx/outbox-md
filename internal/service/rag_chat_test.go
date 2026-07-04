package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/rag"
	"github.com/rajanrx/outbox-md/internal/store"
)

func newKBService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s, func(_, _, _ string) error { return nil }), s
}

func TestAskKBPersistsBothTurnsWithSources(t *testing.T) {
	svc, st := newKBService(t)
	sess, _ := st.CreateSession("proj-a", "chat")

	passages := []rag.Result{
		{DocPath: "docs/spec.md", Heading: "Intro", Snippet: "the answer lives here", Score: 0.8, Source: "hybrid"},
	}
	var gotPrompt string
	svc.SetKBChat(
		func(project, query string, k int) ([]rag.Result, error) {
			if project != "proj-a" {
				t.Fatalf("search project = %q, want proj-a", project)
			}
			return passages, nil
		},
		func(project, prompt string) (string, error) {
			gotPrompt = prompt
			return "Grounded answer (source: docs/spec.md)", nil
		},
	)

	msg, err := svc.AskKB("", sess.ID, "where is the answer?")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != store.RagRoleAssistant || !strings.Contains(msg.Content, "Grounded answer") {
		t.Fatalf("assistant message wrong: %+v", msg)
	}
	if len(msg.Sources) != 1 || msg.Sources[0].DocPath != "docs/spec.md" {
		t.Fatalf("assistant sources not carried: %+v", msg.Sources)
	}

	// The prompt handed to the LLM must contain the question AND a retrieved passage.
	if !strings.Contains(gotPrompt, "where is the answer?") {
		t.Fatalf("prompt missing the question:\n%s", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "docs/spec.md") || !strings.Contains(gotPrompt, "the answer lives here") {
		t.Fatalf("prompt missing the retrieved passage:\n%s", gotPrompt)
	}

	// Both turns are persisted, in order.
	msgs, _ := st.ListMessages(sess.ID)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	if msgs[0].Role != store.RagRoleUser || msgs[0].Content != "where is the answer?" {
		t.Fatalf("user turn wrong: %+v", msgs[0])
	}
	if msgs[1].Role != store.RagRoleAssistant {
		t.Fatalf("assistant turn wrong: %+v", msgs[1])
	}
}

func TestAskKBLLMErrorDoesNotCorruptSession(t *testing.T) {
	svc, st := newKBService(t)
	sess, _ := st.CreateSession("proj-a", "chat")

	svc.SetKBChat(
		func(_ string, _ string, _ int) ([]rag.Result, error) {
			return []rag.Result{{DocPath: "d.md", Snippet: "ctx"}}, nil
		},
		func(_ string, _ string) (string, error) {
			return "", errors.New("spawn: signal killed")
		},
	)

	if _, err := svc.AskKB("", sess.ID, "q?"); err == nil {
		t.Fatal("expected AskKB to surface the LLM error")
	}
	// Nothing persisted — the session is clean (no dangling user turn).
	msgs, _ := st.ListMessages(sess.ID)
	if len(msgs) != 0 {
		t.Fatalf("messages after failed ask = %d, want 0", len(msgs))
	}
}

func TestAskKBUnwiredReturnsError(t *testing.T) {
	svc, st := newKBService(t)
	sess, _ := st.CreateSession("proj-a", "chat")
	if _, err := svc.AskKB("", sess.ID, "q?"); !errors.Is(err, ErrKBNotWired) {
		t.Fatalf("err = %v, want ErrKBNotWired", err)
	}
}

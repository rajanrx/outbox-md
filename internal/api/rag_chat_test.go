package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rajanrx/outbox-md/internal/rag"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
)

// ragChatServer builds an API handler with the Ask-the-KB seams stubbed: search
// returns one fixed passage and the LLM echoes a canned grounded answer.
func ragChatServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	s, _ := store.Open(":memory:")
	t.Cleanup(func() { _ = s.Close() })
	svc := service.New(s, func(_, _, _ string) error { return nil })
	svc.SetKBChat(
		func(_ string, _ string, _ int) ([]rag.Result, error) {
			return []rag.Result{{DocPath: "docs/a.md", Heading: "A", Snippet: "grounding text", Score: 1}}, nil
		},
		func(_ string, _ string) (string, error) { return "answer grounded in docs/a.md", nil },
	)
	return NewAPI(svc, s, sse.NewHub()), s
}

func TestRagChatCreateAskAndListMessages(t *testing.T) {
	h, _ := ragChatServer(t)

	// Create a session.
	rec := do(t, h, http.MethodPost, "/api/rag/sessions", `{"project":"proj-a","title":"My chat"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var sess store.RagSession
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.Title != "My chat" {
		t.Fatalf("created session wrong: %+v", sess)
	}

	// Ask a question → assistant message with sources.
	rec = do(t, h, http.MethodPost, "/api/rag/sessions/"+sess.ID+"/ask", `{"question":"what is A?"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ask: %d %s", rec.Code, rec.Body.String())
	}
	var ans store.RagMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	if ans.Role != store.RagRoleAssistant || ans.Content == "" {
		t.Fatalf("answer wrong: %+v", ans)
	}
	if len(ans.Sources) != 1 || ans.Sources[0].DocPath != "docs/a.md" {
		t.Fatalf("answer sources wrong: %+v", ans.Sources)
	}

	// Messages list shows both the user question and the assistant answer.
	rec = do(t, h, http.MethodGet, "/api/rag/sessions/"+sess.ID+"/messages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("messages: %d %s", rec.Code, rec.Body.String())
	}
	var msgs []store.RagMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &msgs); err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != store.RagRoleUser || msgs[1].Role != store.RagRoleAssistant {
		t.Fatalf("turns wrong: %+v", msgs)
	}
}

func TestRagChatListSessionsByProjectAndDelete(t *testing.T) {
	h, _ := ragChatServer(t)

	do(t, h, http.MethodPost, "/api/rag/sessions", `{"project":"proj-a"}`)
	do(t, h, http.MethodPost, "/api/rag/sessions", `{"project":"proj-b"}`)

	// List proj-a → exactly one; delete it → empty.
	rec := do(t, h, http.MethodGet, "/api/rag/sessions?project=proj-a", "")
	var sessions []store.RagSession
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("proj-a sessions = %d, want 1", len(sessions))
	}
	// A blank title defaulted to "New chat".
	if sessions[0].Title != "New chat" {
		t.Fatalf("default title = %q, want New chat", sessions[0].Title)
	}

	rec = do(t, h, http.MethodDelete, "/api/rag/sessions/"+sessions[0].ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/api/rag/sessions?project=proj-a", "")
	sessions = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &sessions)
	if len(sessions) != 0 {
		t.Fatalf("proj-a sessions after delete = %d, want 0", len(sessions))
	}
}

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

func TestDocAndCommentEndpoints(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	h := NewAPI(svc, s)

	// list docs
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "spec.md") {
		t.Fatalf("list docs: %d %s", rec.Code, rec.Body.String())
	}

	// post a comment on "world"
	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"start":6,"end":11}`)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/docs/"+doc.ID+"/comments", body))
	if rec.Code != 200 {
		t.Fatalf("post comment: %d %s", rec.Code, rec.Body.String())
	}

	// get doc → includes content + the comment
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/docs/"+doc.ID, nil))
	var got struct {
		Content  string            `json:"content"`
		Comments []json.RawMessage `json:"comments"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Content != "Hello world" || len(got.Comments) != 1 {
		t.Fatalf("get doc: content=%q comments=%d", got.Content, len(got.Comments))
	}
}

func TestDevClaimAndPropose(t *testing.T) {
	t.Setenv("OUTBOX_DEV", "1")
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	doc, _, _ := s.CreateDocument("spec.md", "Hello world", "human")
	c, _ := svc.PostComment(doc.ID, domain.Anchor{Start: 0, End: 5}, "human")
	h := NewAPI(svc, s)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/dev/claim", strings.NewReader(`{"commentIds":["`+c.ID+`"]}`)))
	if rec.Code != 200 {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body.String())
	}
	var cl struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cl)

	rec = httptest.NewRecorder()
	body := `{"commentId":"` + c.ID + `","token":"` + cl.Token + `","content":"Say Hello world"}`
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/dev/propose", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("propose: %d %s", rec.Code, rec.Body.String())
	}
}

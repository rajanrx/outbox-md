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

func TestApproveEndpointPinsBaseline(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s)
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/docs/"+doc.ID+"/approve", strings.NewReader(`{"note":"ok"}`))
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("approve status = %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := s.GetDocument(doc.ID)
	if got.Status != domain.DocApproved {
		t.Errorf("status = %q, want approved", got.Status)
	}

	// Re-approve with nothing pending is a 400.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/docs/"+doc.ID+"/reapprove", nil)
	h.ServeHTTP(rr2, req2)
	if rr2.Code != 400 {
		t.Errorf("reapprove status = %d, want 400", rr2.Code)
	}
}

func TestDocViewIncludesBaselineContent(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	h := NewAPI(svc, s)
	doc, _, _ := s.CreateDocument("a.md", "v1", "human")
	rrA := httptest.NewRecorder()
	h.ServeHTTP(rrA, httptest.NewRequest("POST", "/api/docs/"+doc.ID+"/approve", nil))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/docs/"+doc.ID, nil))
	var view struct {
		BaselineContent string `json:"baselineContent"`
		Document        struct {
			Status string `json:"status"`
		} `json:"document"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.BaselineContent != "v1" {
		t.Errorf("baselineContent = %q, want v1", view.BaselineContent)
	}
	if view.Document.Status != "approved" {
		t.Errorf("status = %q, want approved", view.Document.Status)
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

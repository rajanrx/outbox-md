package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
)

// captured holds what the test server received, pushed onto a channel so the
// asserting goroutine never races the async delivery.
type captured struct {
	method string
	event  string
	sig    string
	body   []byte
}

func TestHTTPNotifierDelivers(t *testing.T) {
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- captured{
			method: r.Method,
			event:  r.Header.Get("X-Outbox-Event"),
			sig:    r.Header.Get("X-Outbox-Signature"),
			body:   b,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(config.WebhookConfig{URL: srv.URL}) // no secret, all events
	payload := Event{
		Event: EventCommentCreated, DocID: "d1", DocPath: "spec.md",
		CommentID: "c1", Anchor: &domain.Anchor{Start: 0, End: 5},
		Excerpt: "Hello", TS: "2026-06-30T00:00:00Z",
	}
	n.Fire(EventCommentCreated, payload)

	c := waitFor(t, got)
	if c.method != http.MethodPost {
		t.Errorf("method = %q, want POST", c.method)
	}
	if c.event != EventCommentCreated {
		t.Errorf("X-Outbox-Event = %q, want %q", c.event, EventCommentCreated)
	}
	if c.sig != "" {
		t.Errorf("X-Outbox-Signature = %q, want empty (no secret)", c.sig)
	}
	var rt Event
	if err := json.Unmarshal(c.body, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.DocID != "d1" || rt.CommentID != "c1" || rt.Excerpt != "Hello" {
		t.Errorf("round-trip = %+v, want d1/c1/Hello", rt)
	}
	if rt.Anchor == nil || rt.Anchor.End != 5 {
		t.Errorf("anchor = %+v, want {0,5}", rt.Anchor)
	}
}

func TestHTTPNotifierSignsWithSecret(t *testing.T) {
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- captured{sig: r.Header.Get("X-Outbox-Signature"), body: b}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	const secret = "topsecret"
	n := New(config.WebhookConfig{URL: srv.URL, Secret: secret})
	n.Fire(EventDocumentApprove, Event{Event: EventDocumentApprove, DocID: "d1"})

	c := waitFor(t, got)
	// Expected signature computed over the EXACT bytes the server received.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(c.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if c.sig != want {
		t.Errorf("signature = %q, want %q", c.sig, want)
	}
}

func TestNewReturnsNopWhenNoURL(t *testing.T) {
	if _, ok := New(config.WebhookConfig{}).(Nop); !ok {
		t.Fatal("expected Nop notifier when URL is empty")
	}
}

func TestDisabledEventIsNotDelivered(t *testing.T) {
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- captured{event: r.Header.Get("X-Outbox-Event"), body: b}
	}))
	defer srv.Close()

	// Only comment.created is enabled; firing document.approved must be a no-op.
	n := New(config.WebhookConfig{URL: srv.URL, Events: []string{EventCommentCreated}})
	n.Fire(EventDocumentApprove, Event{Event: EventDocumentApprove, DocID: "d1"})

	select {
	case c := <-got:
		t.Fatalf("disabled event was delivered: %q", c.event)
	case <-time.After(300 * time.Millisecond):
		// expected: nothing delivered
	}
}

func waitFor(t *testing.T, ch <-chan captured) captured {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no webhook request received")
		return captured{}
	}
}

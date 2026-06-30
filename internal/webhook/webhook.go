// Package webhook delivers governance events (comment.created, comment.replied,
// comment.resolved, document.approved) to an external HTTP runner. Delivery is
// best-effort and fully decoupled from the request path: Fire never blocks the
// caller, never returns an error, and never panics.
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/domain"
)

// Event names. These string values must match the defaults listed in
// config.Defaults (which cannot import this package without a cycle).
const (
	EventCommentCreated  = "comment.created"
	EventCommentReplied  = "comment.replied"
	EventCommentResolved = "comment.resolved"
	EventDocumentApprove = "document.approved"
)

// Event is the JSON payload POSTed to the webhook URL. Comment events carry the
// anchored excerpt and thread so the runner can act without a follow-up read;
// document.approved carries only the document identity.
type Event struct {
	Event     string                 `json:"event"`
	DocID     string                 `json:"docId"`
	DocPath   string                 `json:"docPath"`
	CommentID string                 `json:"commentId,omitempty"`
	Anchor    *domain.Anchor         `json:"anchor,omitempty"`
	Excerpt   string                 `json:"excerpt,omitempty"`
	Thread    []domain.ThreadMessage `json:"thread,omitempty"`
	TS        string                 `json:"ts"`
}

// Notifier is the seam the service depends on. The default is Nop.
type Notifier interface {
	Fire(event string, payload any)
}

// Nop is the no-op notifier used when no webhook URL is configured.
type Nop struct{}

func (Nop) Fire(string, any) {}

// fanout is a Notifier that delivers each event to several sinks in turn.
type fanout []Notifier

// Fire fans the event out to every non-nil notifier in order.
func (f fanout) Fire(event string, payload any) {
	for _, n := range f {
		if n != nil {
			n.Fire(event, payload)
		}
	}
}

// Fanout returns a Notifier that fires every non-nil notifier in ns. It lets one
// governance event reach multiple sinks — e.g. the HTTP webhook (machine/runner)
// and the SSE hub (browser) — through the single SetWebhook seam.
func Fanout(ns ...Notifier) Notifier { return fanout(ns) }

// HTTPNotifier POSTs events to a configured URL, optionally HMAC-signed.
type HTTPNotifier struct {
	URL    string
	Secret string
	Events map[string]bool // empty ⇒ all events allowed
	Client *http.Client
}

// New returns a Nop when cfg.URL is empty, otherwise an HTTPNotifier with a 5s
// client timeout. An empty cfg.Events means every event is allowed.
func New(cfg config.WebhookConfig) Notifier {
	if cfg.URL == "" {
		return Nop{}
	}
	events := make(map[string]bool, len(cfg.Events))
	for _, e := range cfg.Events {
		events[e] = true
	}
	return &HTTPNotifier{
		URL:    cfg.URL,
		Secret: cfg.Secret,
		Events: events,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

// allowed reports whether the event is enabled. An empty event set allows all.
func (n *HTTPNotifier) allowed(event string) bool {
	return len(n.Events) == 0 || n.Events[event]
}

// Fire marshals the payload and delivers it on a background goroutine. It is a
// no-op for disabled events and for payloads that fail to marshal.
func (n *HTTPNotifier) Fire(event string, payload any) {
	if !n.allowed(event) {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhook: marshal %s: %v", event, err)
		return
	}
	go n.deliver(event, body)
}

// deliver POSTs the body, retrying up to twice on error or non-2xx with a short
// backoff. The signature, when a secret is set, is over the exact body bytes.
func (n *HTTPNotifier) deliver(event string, body []byte) {
	var sig string
	if n.Secret != "" {
		mac := hmac.New(sha256.New, []byte(n.Secret))
		mac.Write(body)
		sig = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	backoffs := []time.Duration{200 * time.Millisecond, 800 * time.Millisecond}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		if attempt > 0 {
			time.Sleep(backoffs[attempt-1])
		}
		req, err := http.NewRequest(http.MethodPost, n.URL, bytes.NewReader(body))
		if err != nil {
			lastErr = err
			break // a malformed URL will never succeed — don't retry
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Outbox-Event", event)
		if sig != "" {
			req.Header.Set("X-Outbox-Signature", sig)
		}
		resp, err := n.Client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
		lastErr = fmt.Errorf("status %d", resp.StatusCode)
	}
	log.Printf("webhook: %s delivery failed after retries: %v", event, lastErr)
}

// Package sse provides an in-process pub/sub hub that fans governance events out
// to connected browsers over Server-Sent Events. The Hub satisfies
// webhook.Notifier (structurally), so it can be wired in alongside the HTTP
// webhook notifier: every governance event reaches both the external runner and
// every open browser stream. Delivery is best-effort and non-blocking — a slow
// or full subscriber is skipped, never waited on, exactly like the webhook.
package sse

import (
	"encoding/json"
	"sync"
)

// Message is one event ready to write to an SSE stream: the event name and the
// already-marshalled JSON payload.
type Message struct {
	Event string
	Data  []byte
}

// Hub manages the set of active subscriber channels behind a mutex.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
}

// NewHub returns an empty Hub with no subscribers.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan Message]struct{})}
}

// Subscribe registers a new subscriber and returns its receive channel plus an
// idempotent unsubscribe func. The channel is buffered (cap 16) so a transient
// burst doesn't drop events; once full, Fire skips this subscriber. The caller
// must invoke the returned func (typically via defer) when the stream ends.
func (h *Hub) Subscribe() (<-chan Message, func()) {
	ch := make(chan Message, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			close(ch)
			h.mu.Unlock()
		})
	}
	return ch, unsub
}

// Fire marshals payload and broadcasts it to every subscriber, non-blocking. It
// implements webhook.Notifier. A subscriber whose buffer is full is skipped
// (best-effort delivery); a payload that fails to marshal is dropped. Holding
// the lock here serializes against Subscribe/unsubscribe, so a send can never
// race a channel close.
func (h *Hub) Fire(event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := Message{Event: event, Data: data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // full buffer → skip, don't block the firing goroutine
		}
	}
}

// Enabled reports whether the hub has at least one live subscriber. It
// implements webhook.Notifier so the service can skip building events nobody is
// streaming; a browser that reconnects does a full refresh, so events fired
// while no one was subscribed are intentionally not retained.
func (h *Hub) Enabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

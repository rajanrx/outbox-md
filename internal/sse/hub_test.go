package sse

import (
	"testing"
	"time"
)

// recv reads one message within a short deadline, failing the test on timeout so
// a missed delivery can't hang the suite.
func recv(t *testing.T, ch <-chan Message) Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return Message{}
	}
}

// TestFireDeliversToSubscriber checks a single subscriber receives the event
// with the right name and marshalled payload.
func TestFireDeliversToSubscriber(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe()
	defer unsub()

	h.Fire("comment.created", map[string]string{"docId": "abc"})

	m := recv(t, ch)
	if m.Event != "comment.created" {
		t.Errorf("event = %q, want comment.created", m.Event)
	}
	if string(m.Data) != `{"docId":"abc"}` {
		t.Errorf("data = %s, want JSON payload", m.Data)
	}
}

// TestFireBroadcastsToAll checks a second subscriber also receives the event.
func TestFireBroadcastsToAll(t *testing.T) {
	h := NewHub()
	a, unsubA := h.Subscribe()
	defer unsubA()
	b, unsubB := h.Subscribe()
	defer unsubB()

	h.Fire("comment.resolved", map[string]int{"n": 1})

	if got := recv(t, a).Event; got != "comment.resolved" {
		t.Errorf("subscriber A event = %q", got)
	}
	if got := recv(t, b).Event; got != "comment.resolved" {
		t.Errorf("subscriber B event = %q", got)
	}
}

// TestUnsubscribeStopsDelivery checks an unsubscribed channel no longer receives
// events (and is closed).
func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe()
	unsub()

	h.Fire("document.approved", map[string]string{"docId": "x"})

	// The channel is closed and empty: a receive returns the zero value, not ok.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("received a message after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("closed channel did not return")
	}
}

// TestFireDoesNotBlockOnFullBuffer fills a subscriber's buffer past capacity and
// asserts Fire still returns promptly (the full subscriber is skipped).
func TestFireDoesNotBlockOnFullBuffer(t *testing.T) {
	h := NewHub()
	_, unsub := h.Subscribe() // never drained
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ { // >> buffer cap of 16
			h.Fire("comment.created", map[string]int{"i": i})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Fire blocked on a full subscriber buffer")
	}
}

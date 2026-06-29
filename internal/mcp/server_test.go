package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/store"
)

// The process_outbox prompt is the agent workflow shipped with the server. It
// must be discoverable via prompts/list and return the guidance via prompts/get.
func TestServerRegistersProcessOutboxPrompt(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	svc := service.New(s, func(_, _ string) error { return nil })
	srv := NewServer(&Handlers{Svc: svc, St: s})

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	found := false
	for p, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if p.Name == "process_outbox" {
			found = true
		}
	}
	if !found {
		t.Fatal("prompts/list did not include process_outbox")
	}

	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "process_outbox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) == 0 {
		t.Fatal("process_outbox returned no messages")
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(text, "Work the queue IN ORDER") {
		t.Errorf("prompt text missing workflow guidance: %q", text)
	}
}

// Command outbox-runner is a reference webhook runner for outbox-md: the
// client-side, bring-your-own-agent counterpart to the interactive MCP. It
// receives outbox-md webhooks, verifies them, and drives a single-agent loop
// (claim → propose/reply) over the outbox-md MCP tools.
//
// See examples/runner/README.md for setup, the two agent modes, and the cost
// note. This is a single-agent loop, not the council fan-out.
package main

import (
	"log"
	"net/http"
	"time"
)

// newBackend selects the agent backend from config.
func newBackend(cfg Config) Backend {
	switch cfg.AgentMode {
	case "api":
		return &APIBackend{
			MCPURL:  cfg.MCPURL,
			APIKey:  cfg.APIKey,
			Model:   cfg.Model,
			AgentID: cfg.AgentID,
		}
	default: // "cli"
		return &CLIBackend{
			CmdTemplate: cfg.AgentCmd,
			Prompt:      cfg.Prompt,
			Timeout:     10 * time.Minute,
		}
	}
}

func main() {
	cfg := LoadConfig()
	server := NewServer(cfg, newBackend(cfg))

	signing := "default-deny (no secret set — refusing unsigned)"
	switch {
	case cfg.Secret != "":
		signing = "on (HMAC-SHA256 enforced)"
	case cfg.AllowUnsigned:
		signing = "off (RUNNER_ALLOW_UNSIGNED set — accepting UNSIGNED, NOT recommended)"
	default:
		log.Printf("runner: refusing unsigned webhooks; set OUTBOX_WEBHOOK_SECRET, " +
			"or RUNNER_ALLOW_UNSIGNED=1 to allow unsigned (NOT recommended).")
	}
	log.Printf("outbox-runner listening on %s", cfg.Addr)
	log.Printf("  agent mode : %s", cfg.AgentMode)
	log.Printf("  signing    : %s", signing)
	log.Printf("  events     : %v", cfg.Events)
	log.Printf("  debounce   : %s", cfg.Debounce)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(httpServer.ListenAndServe())
}

package config

import (
	"log"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Agent    AgentConfig    `json:"agent"    yaml:"agent"`
	Approval ApprovalConfig `json:"approval" yaml:"approval"`
	Webhook  WebhookConfig  `json:"webhook"  yaml:"webhook"`
}

type AgentConfig struct {
	BatchSize int `json:"batchSize" yaml:"batch_size"`
}

type ApprovalConfig struct {
	PostApprovalComments bool `json:"postApprovalComments" yaml:"post_approval_comments"`
}

// WebhookConfig points outbox at an external runner that should be notified of
// governance events. An empty URL disables webhooks (a no-op notifier is used).
// An empty Events slice means "all events enabled".
type WebhookConfig struct {
	URL    string   `json:"url"    yaml:"url"`
	Secret string   `json:"secret" yaml:"secret"`
	Events []string `json:"events" yaml:"events"`
}

// Defaults is the built-in configuration used when no outbox.yaml is present,
// and the floor every loaded config layers over.
func Defaults() Config {
	return Config{
		Agent:    AgentConfig{BatchSize: 5},
		Approval: ApprovalConfig{PostApprovalComments: true},
		// All four governance events are enabled by default. These string
		// literals mirror the event names emitted by internal/webhook; they are
		// duplicated here (rather than imported) to keep config free of a webhook
		// dependency.
		Webhook: WebhookConfig{Events: []string{
			"comment.created", "comment.replied", "comment.resolved", "document.approved",
		}},
	}
}

// Load reads outbox.yaml from the folder root, layered over Defaults(). A
// missing file yields the defaults; a malformed file logs and falls back to the
// defaults (startup never fails on config). batch_size below 1 is corrected.
func Load(dir string) Config {
	cfg := Defaults()
	// Layer outbox.yaml over the defaults when it exists; a missing or malformed
	// file just leaves the defaults in place (startup never fails on config).
	if data, err := os.ReadFile(filepath.Join(dir, "outbox.yaml")); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			log.Printf("outbox.yaml: invalid, using defaults: %v", err)
			cfg = Defaults()
		} else if cfg.Agent.BatchSize < 1 {
			cfg.Agent.BatchSize = Defaults().Agent.BatchSize
		}
	}
	// Environment overrides win over the file — and MUST apply even when there is
	// no outbox.yaml (env-only config is the common case for a containerized
	// server). Previously these sat behind the no-file early return and were
	// silently skipped, so OUTBOX_WEBHOOK_URL/SECRET were ignored without a yaml.
	if v := os.Getenv("OUTBOX_WEBHOOK_URL"); v != "" {
		cfg.Webhook.URL = v
	}
	if v := os.Getenv("OUTBOX_WEBHOOK_SECRET"); v != "" {
		cfg.Webhook.Secret = v
	}
	return cfg
}

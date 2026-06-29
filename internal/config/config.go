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
}

type AgentConfig struct {
	BatchSize int `json:"batchSize" yaml:"batch_size"`
}

type ApprovalConfig struct {
	PostApprovalComments bool `json:"postApprovalComments" yaml:"post_approval_comments"`
}

// Defaults is the built-in configuration used when no outbox.yaml is present,
// and the floor every loaded config layers over.
func Defaults() Config {
	return Config{
		Agent:    AgentConfig{BatchSize: 5},
		Approval: ApprovalConfig{PostApprovalComments: true},
	}
}

// Load reads outbox.yaml from the folder root, layered over Defaults(). A
// missing file yields the defaults; a malformed file logs and falls back to the
// defaults (startup never fails on config). batch_size below 1 is corrected.
func Load(dir string) Config {
	cfg := Defaults()
	data, err := os.ReadFile(filepath.Join(dir, "outbox.yaml"))
	if err != nil {
		return cfg // not present / unreadable → defaults
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("outbox.yaml: invalid, using defaults: %v", err)
		return Defaults()
	}
	if cfg.Agent.BatchSize < 1 {
		cfg.Agent.BatchSize = Defaults().Agent.BatchSize
	}
	return cfg
}

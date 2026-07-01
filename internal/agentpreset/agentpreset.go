// Package agentpreset maps short preset names to the full agent command template
// the auto-reply engine spawns for a project. A template contains the literal
// {prompt} token, which the engine replaces with the instruction as a single
// argv element (no shell). The --agent-cmd escape hatch lets a user supply any
// command directly, overriding these presets.
package agentpreset

import (
	"fmt"
	"sort"
)

// presets maps a preset name to its command template.
//
//   - claude  — VERIFIED: this is outbox-md's built-in default; the Claude Code
//     CLI reads outbox-md's MCP over --allowedTools.
//   - codex   — ASSUMED: OpenAI Codex CLI reads its own ~/.codex/config.toml for
//     MCP servers, so no --allowedTools flag; adjust with --agent-cmd if your
//     Codex invocation differs.
//   - copilot — ASSUMED: GitHub Copilot CLI headless prompt; adjust with
//     --agent-cmd if your Copilot invocation differs.
var presets = map[string]string{
	"claude":  "claude -p {prompt} --allowedTools mcp__outbox-md__*",
	"codex":   "codex exec {prompt}",
	"copilot": "copilot -p {prompt}",
}

// Resolve returns the command template for a known preset. The bool is false for
// an unknown preset name.
func Resolve(preset string) (string, bool) {
	cmd, ok := presets[preset]
	return cmd, ok
}

// Names returns the known preset names, sorted, for help text and errors.
func Names() []string {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ResolveOrError resolves a preset, returning a helpful error listing the known
// names when it is unknown.
func ResolveOrError(preset string) (string, error) {
	if cmd, ok := Resolve(preset); ok {
		return cmd, nil
	}
	return "", fmt.Errorf("unknown agent preset %q (known: %v)", preset, Names())
}

// Package agentpreset maps short preset names to the full agent command template
// the auto-reply engine spawns for a project. A template contains the literal
// {prompt} token, which the engine replaces with the instruction as a single
// argv element (no shell). The --agent-cmd escape hatch lets a user supply any
// command directly, overriding these presets.
package agentpreset

import (
	"fmt"
	"sort"
	"strings"
)

// presets maps a preset name to its command template.
//
//   - claude  — VERIFIED: this is outbox-md's built-in default; the Claude Code
//     CLI reads outbox-md's MCP over --allowedTools.
//   - codex   — OpenAI Codex CLI reads its own ~/.codex/config.toml for MCP
//     servers. `exec` (non-interactive) with the default workspace-write sandbox
//     CANCELS MCP tool calls — there is no human to approve them — so the council
//     member/chair can't reach outbox's MCP. --dangerously-bypass-approvals-and-
//     sandbox runs it fully autonomous so the MCP calls complete. This is scoped
//     to council-spawned codex (not the user's interactive codex) and is safe for
//     a trusted local repo where agents only PROPOSE (never write files); adjust
//     with --agent-cmd if you want a narrower sandbox.
//   - copilot — ASSUMED: GitHub Copilot CLI headless prompt; adjust with
//     --agent-cmd if your Copilot invocation differs.
var presets = map[string]string{
	"claude":  "claude -p {prompt} --allowedTools mcp__outbox-md__*",
	"codex":   "codex exec --dangerously-bypass-approvals-and-sandbox {prompt}",
	"copilot": "copilot -p {prompt}",
}

// modelTemplates is the per-preset command template WITH a model flag, used when a
// member carries a Model. The literal "<model>" token is replaced with the model
// string; "{prompt}" is left for the engine to substitute (as in presets).
//
// Flag names are VERIFIED against each CLI's --help:
//   - claude  → `--model <model>` (Claude Code: "--model <model>  Model for the
//     current session").
//   - codex   → `-m <model>` (Codex: "-m, --model <MODEL>  Model the agent should
//     use").
//   - copilot → `--model <model>` (Copilot: "--model <model>  Set the AI model to
//     use").
//
// A raw --agent-cmd command is never resolved through here (the user embedded the
// model themselves); only a preset name reaches this map.
var modelTemplates = map[string]string{
	"claude":  "claude --model <model> -p {prompt} --allowedTools mcp__outbox-md__*",
	"codex":   "codex exec --dangerously-bypass-approvals-and-sandbox -m <model> {prompt}",
	"copilot": "copilot --model <model> -p {prompt}",
}

// Resolve returns the command template for a known preset. The bool is false for
// an unknown preset name.
func Resolve(preset string) (string, bool) {
	cmd, ok := presets[preset]
	return cmd, ok
}

// ResolveModel returns the command template for a known preset with the model flag
// injected. An empty (or whitespace) model delegates to Resolve — the flag is
// omitted entirely, so a member with no model behaves exactly as before. The bool
// is false for an unknown preset name (the caller then treats the agent as a raw
// command and uses it verbatim, ignoring the model).
func ResolveModel(preset, model string) (string, bool) {
	if strings.TrimSpace(model) == "" {
		return Resolve(preset)
	}
	tmpl, ok := modelTemplates[preset]
	if !ok {
		return "", false
	}
	return strings.ReplaceAll(tmpl, "<model>", strings.TrimSpace(model)), true
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

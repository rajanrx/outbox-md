// Package mcpclients detects installed AI clients and registers the outbox-md
// MCP endpoint with each of them. It is deliberately pure and injectable: all
// filesystem, environment and process access is threaded through an Env value so
// the core logic (detection, config merging) is unit-testable without touching
// the real home directory, PATH, or spawning real CLIs.
//
// Each supported client is a descriptor that knows how to DETECT itself (a
// command on PATH or a config directory that exists) and how to REGISTER the
// outbox-md server (either by running a CLI command, or by merging a native
// config file). Registration is always idempotent: an existing config is parsed
// with a real parser, only the "outbox-md" entry is added or replaced, and every
// other setting is preserved. (The Codex TOML writer re-marshals via a TOML
// library, which preserves all other tables/keys but not comments; the JSON
// writers preserve everything.)
package mcpclients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// serverName is the key/table name used for the outbox-md MCP entry across every
// client config.
const serverName = "outbox-md"

// Env is the injected environment the package operates against. Production wiring
// fills these from the os/exec packages; tests supply fakes. All fields are
// required by at least one client; a nil func will panic if that client is
// attempted, which is a programming error, not a runtime condition.
type Env struct {
	// HomeDir is the user's home directory (e.g. os.UserHomeDir()).
	HomeDir string
	// GOOS is the target OS (e.g. runtime.GOOS); it selects OS-specific config
	// locations such as Claude Desktop's.
	GOOS string
	// CommandExists reports whether a binary is on PATH (e.g. exec.LookPath).
	CommandExists func(name string) bool
	// DirExists reports whether path exists and is a directory.
	DirExists func(path string) bool
	// ReadFile reads a config file; it should return an fs.ErrNotExist-wrapped
	// error when the file is absent (as os.ReadFile does).
	ReadFile func(path string) ([]byte, error)
	// WriteFile writes a config file.
	WriteFile func(path string, data []byte, perm fs.FileMode) error
	// MkdirAll creates a directory tree.
	MkdirAll func(path string, perm fs.FileMode) error
	// RunCommand runs a CLI command (used only by command-registered clients).
	RunCommand func(name string, args []string) error
}

// Action is the outcome category for a single client.
type Action string

const (
	// ActionWired means the MCP was registered (config written or command ran).
	ActionWired Action = "wired"
	// ActionNoted means registration was attempted and succeeded-enough, but
	// there is a note for the user (e.g. the Claude CLI was absent so the manual
	// command is printed, or it was already registered).
	ActionNoted Action = "noted"
	// ActionSkipped means the client was not detected and not forced, so nothing
	// was done. This is never an error.
	ActionSkipped Action = "skipped"
	// ActionFailed means registration was attempted and returned an error. init
	// still exits 0 — one client failing must never fail the whole command.
	ActionFailed Action = "failed"
)

// Result is the per-client outcome of a Register call.
type Result struct {
	Slug     string
	Name     string
	Detected bool
	Action   Action
	// Detail is the config file written or the command run, for display.
	Detail string
	// Note is a human-readable hint (populated for ActionNoted).
	Note string
	// Err is set for ActionFailed.
	Err error
}

// Options controls which clients Register targets.
type Options struct {
	// All attempts every supported client regardless of detection (writes
	// configs even if the app is not installed yet).
	All bool
	// Only, when non-empty, restricts the run to these client slugs
	// (case-insensitive). Targeted clients are attempted regardless of
	// detection. Unknown slugs make Register return an error.
	Only []string
}

// regOutcome is a client register function's success result.
type regOutcome struct {
	Detail string
	Note   string
}

type registerFn func(env Env, url string) (regOutcome, error)

// Client is a descriptor for one AI client.
type Client struct {
	Slug string
	Name string

	detect     func(env Env) bool
	configPath func(env Env) string // "" for command-registered clients
	register   registerFn
}

// Detect reports whether the client is installed in env.
func (c Client) Detect(env Env) bool { return c.detect(env) }

// ConfigPath returns the client's config file path, or "" if it registers via a
// command rather than a file.
func (c Client) ConfigPath(env Env) string {
	if c.configPath == nil {
		return ""
	}
	return c.configPath(env)
}

// Clients returns the supported client descriptors in a stable, display order.
func Clients() []Client {
	return []Client{
		{
			Slug:     "claude-code",
			Name:     "Claude Code",
			detect:   func(env Env) bool { return env.CommandExists("claude") },
			register: registerClaudeCode,
		},
		{
			Slug:       "gemini",
			Name:       "Gemini CLI",
			detect:     func(env Env) bool { return env.CommandExists("gemini") },
			configPath: geminiPath,
			// Gemini CLI supports native HTTP transport via the httpUrl key.
			register: jsonRegister(geminiPath, func(url string) map[string]any {
				return map[string]any{"httpUrl": url}
			}),
		},
		{
			Slug:       "cursor",
			Name:       "Cursor",
			detect:     func(env Env) bool { return env.DirExists(filepath.Join(env.HomeDir, ".cursor")) },
			configPath: cursorPath,
			register: jsonRegister(cursorPath, func(url string) map[string]any {
				return map[string]any{"url": url}
			}),
		},
		{
			Slug:       "windsurf",
			Name:       "Windsurf",
			detect:     func(env Env) bool { return env.DirExists(filepath.Join(env.HomeDir, ".codeium", "windsurf")) },
			configPath: windsurfPath,
			register: jsonRegister(windsurfPath, func(url string) map[string]any {
				return map[string]any{"serverUrl": url}
			}),
		},
		{
			Slug:       "claude-desktop",
			Name:       "Claude Desktop",
			detect:     func(env Env) bool { return env.DirExists(claudeDesktopDir(env)) },
			configPath: claudeDesktopPath,
			// Claude Desktop is stdio-only, so bridge over HTTP with mcp-remote.
			register: jsonRegister(claudeDesktopPath, func(url string) map[string]any {
				return mcpRemoteBridge(url)
			}),
		},
		{
			Slug:       "codex",
			Name:       "OpenAI Codex CLI",
			detect:     func(env Env) bool { return env.CommandExists("codex") },
			configPath: codexPath,
			// Codex speaks native Streamable-HTTP MCP — write the url directly.
			register: codexRegister,
		},
	}
}

// Slugs returns the canonical slugs of all supported clients.
func Slugs() []string {
	cs := Clients()
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Slug
	}
	return out
}

// Register detects and registers the outbox-md MCP (URL url) with the selected
// clients, returning one Result per considered client. It returns an error only
// for a bad Options (an unknown --client slug); individual client failures are
// reported in-band via Result and never abort the run.
func Register(env Env, url string, opts Options) ([]Result, error) {
	clients := Clients()

	selected := clients
	if len(opts.Only) > 0 {
		bySlug := make(map[string]Client, len(clients))
		for _, c := range clients {
			bySlug[c.Slug] = c
		}
		selected = nil
		seen := map[string]bool{}
		for _, raw := range opts.Only {
			slug := strings.ToLower(strings.TrimSpace(raw))
			c, ok := bySlug[slug]
			if !ok {
				return nil, fmt.Errorf("unknown client %q — valid clients: %s", raw, strings.Join(Slugs(), ", "))
			}
			if seen[slug] {
				continue
			}
			seen[slug] = true
			selected = append(selected, c)
		}
	}

	targeted := len(opts.Only) > 0
	results := make([]Result, 0, len(selected))
	for _, c := range selected {
		detected := c.detect(env)
		// Attempt when forced (--all), explicitly targeted (--client), or found.
		attempt := opts.All || targeted || detected

		r := Result{Slug: c.Slug, Name: c.Name, Detected: detected}
		if !attempt {
			r.Action = ActionSkipped
			results = append(results, r)
			continue
		}

		out, err := c.register(env, url)
		switch {
		case err != nil:
			r.Action = ActionFailed
			r.Err = err
		case out.Note != "":
			r.Action = ActionNoted
			r.Note = out.Note
			r.Detail = out.Detail
		default:
			r.Action = ActionWired
			r.Detail = out.Detail
		}
		results = append(results, r)
	}
	return results, nil
}

// --- config paths ---------------------------------------------------------

func geminiPath(env Env) string { return filepath.Join(env.HomeDir, ".gemini", "settings.json") }
func cursorPath(env Env) string { return filepath.Join(env.HomeDir, ".cursor", "mcp.json") }
func windsurfPath(env Env) string {
	return filepath.Join(env.HomeDir, ".codeium", "windsurf", "mcp_config.json")
}
func codexPath(env Env) string { return filepath.Join(env.HomeDir, ".codex", "config.toml") }

// claudeDesktopDir is the OS-specific Claude Desktop config directory.
func claudeDesktopDir(env Env) string {
	if env.GOOS == "darwin" {
		return filepath.Join(env.HomeDir, "Library", "Application Support", "Claude")
	}
	// Linux (and any other non-darwin host we support).
	return filepath.Join(env.HomeDir, ".config", "Claude")
}

func claudeDesktopPath(env Env) string {
	return filepath.Join(claudeDesktopDir(env), "claude_desktop_config.json")
}

// mcpRemoteBridge is the stdio-bridge entry for HTTP-only stdio clients.
func mcpRemoteBridge(url string) map[string]any {
	return map[string]any{
		"command": "npx",
		"args":    []any{"-y", "mcp-remote", url},
	}
}

// --- registration ---------------------------------------------------------

// registerClaudeCode registers via the `claude` CLI. It never returns an error:
// when the CLI is absent or the command fails (e.g. already registered), it
// degrades to a note carrying the exact manual command, so `outbox init` stays
// green and idempotent.
func registerClaudeCode(env Env, url string) (regOutcome, error) {
	args := []string{"mcp", "add", "--transport", "http", serverName, url}
	manual := "claude " + strings.Join(args, " ")

	if env.RunCommand == nil || !env.CommandExists("claude") {
		return regOutcome{Detail: manual, Note: "claude CLI not found — run: " + manual}, nil
	}
	if err := env.RunCommand("claude", args); err != nil {
		return regOutcome{Detail: manual, Note: "already registered or CLI error — run manually: " + manual}, nil
	}
	return regOutcome{Detail: manual}, nil
}

// jsonRegister builds a register func that merges a JSON config file, adding or
// replacing only the outbox-md entry with the value entryFn produces.
func jsonRegister(pathFn func(Env) string, entryFn func(url string) map[string]any) registerFn {
	return func(env Env, url string) (regOutcome, error) {
		path := pathFn(env)
		existing, err := readConfig(env, path)
		if err != nil {
			return regOutcome{}, err
		}
		merged, err := MergeJSON(existing, serverName, entryFn(url))
		if err != nil {
			return regOutcome{}, fmt.Errorf("%s: %w", path, err)
		}
		if err := writeConfig(env, path, merged); err != nil {
			return regOutcome{}, err
		}
		return regOutcome{Detail: path}, nil
	}
}

// codexRegister merges the Codex TOML config, adding or replacing only the
// [mcp_servers.outbox-md] table with the native HTTP url (Codex speaks
// Streamable-HTTP MCP natively, so no mcp-remote/Node bridge is needed). It parses the
// existing config with a real TOML library, so any spec-legal file (multi-line
// strings, trailing header comments, inline/dotted-key table forms) is handled
// correctly. If the existing file is present but is NOT valid TOML, it fails
// safe: the file is left untouched and the user is handed the exact table to add
// by hand, so a hand-edited or corrupt config is never clobbered.
func codexRegister(env Env, url string) (regOutcome, error) {
	path := codexPath(env)
	existing, err := readConfig(env, path)
	if err != nil {
		return regOutcome{}, err
	}
	merged, err := MergeTOML(existing, serverName, map[string]any{"url": url})
	if err != nil {
		return regOutcome{Detail: path}, fmt.Errorf(
			"existing %s is not valid TOML — left untouched to avoid corrupting it; "+
				"add this table manually:\n%s", path, codexManualSnippet(url))
	}
	if err := writeConfig(env, path, merged); err != nil {
		return regOutcome{}, err
	}
	return regOutcome{Detail: path}, nil
}

// codexManualSnippet is the [mcp_servers.outbox-md] table a user can paste into
// ~/.codex/config.toml by hand; printed on the fail-safe (unparseable) path.
func codexManualSnippet(url string) string {
	return fmt.Sprintf("[mcp_servers.%s]\nurl = %q\n", serverName, url)
}

// readConfig reads path, treating a missing file as empty (nil) content. Any
// other read error is returned.
func readConfig(env Env, path string) ([]byte, error) {
	b, err := env.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// writeConfig creates the parent directory if missing, then writes data.
func writeConfig(env Env, path string, data []byte) error {
	if err := env.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return env.WriteFile(path, data, 0o644)
}

// --- merge helpers --------------------------------------------------------

// MergeJSON parses existing (an mcpServers-style JSON config, possibly empty or
// absent), sets mcpServers[name] to entry, preserves every other key and server,
// and returns pretty-printed JSON. An empty or whitespace-only input is treated
// as an empty object.
func MergeJSON(existing []byte, name string, entry map[string]any) ([]byte, error) {
	root := map[string]any{}
	if s := bytes.TrimSpace(existing); len(s) > 0 {
		if err := json.Unmarshal(s, &root); err != nil {
			return nil, fmt.Errorf("parse existing config: %w", err)
		}
	}

	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	root["mcpServers"] = servers

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// MergeTOML parses an existing Codex config.toml (an empty or whitespace-only
// input, or nil, is treated as an empty config), sets mcp_servers[name] to entry,
// preserves every other table and key, and returns re-marshalled TOML. Because it
// parses with a real TOML library and overwrites exactly one key, it is
// inherently idempotent (re-running overwrites the same key, never duplicating a
// table) and lexically correct on spec-legal input that the old line-based strip
// corrupted (multi-line strings, trailing header comments, inline/dotted-key
// forms). It returns an error if existing is present but is NOT valid TOML, so
// callers can refuse to overwrite a corrupt or hand-edited file. Note: a full
// re-marshal does not preserve TOML comments — the deliberate trade for
// guaranteed-valid output.
func MergeTOML(existing []byte, name string, entry map[string]any) ([]byte, error) {
	root := map[string]any{}
	if s := bytes.TrimSpace(existing); len(s) > 0 {
		if err := toml.Unmarshal(existing, &root); err != nil {
			return nil, fmt.Errorf("parse existing config: %w", err)
		}
	}

	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	root["mcp_servers"] = servers

	return toml.Marshal(root)
}

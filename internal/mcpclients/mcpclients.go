// Package mcpclients detects installed AI clients and registers the outbox-md
// MCP endpoint with each of them. It is deliberately pure and injectable: all
// filesystem, environment and process access is threaded through an Env value so
// the core logic (detection, config merging) is unit-testable without touching
// the real home directory, PATH, or spawning real CLIs.
//
// Each supported client is a descriptor that knows how to DETECT itself (a
// command on PATH or a config directory that exists) and how to REGISTER the
// outbox-md server (either by running a CLI command, or by merging a native
// config file). Registration is always idempotent: an existing config is parsed,
// only the "outbox-md" entry is added or replaced, and everything else is
// preserved.
package mcpclients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
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
			// Codex speaks stdio MCP over TOML config, so bridge with mcp-remote.
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
// [mcp_servers.outbox-md] table with the mcp-remote bridge.
func codexRegister(env Env, url string) (regOutcome, error) {
	path := codexPath(env)
	existing, err := readConfig(env, path)
	if err != nil {
		return regOutcome{}, err
	}
	merged := MergeTOML(existing, serverName, url)
	if err := writeConfig(env, path, merged); err != nil {
		return regOutcome{}, err
	}
	return regOutcome{Detail: path}, nil
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

// MergeTOML adds or replaces the [mcp_servers.<name>] table (an mcp-remote bridge
// to url) in an existing TOML config, preserving all other content. It strips any
// prior [mcp_servers.<name>] table and its subtables, then appends a fresh one,
// so re-running is idempotent (replace, never duplicate).
func MergeTOML(existing []byte, name, url string) []byte {
	table := "mcp_servers." + name
	block := "[" + table + "]\n" +
		"command = \"npx\"\n" +
		fmt.Sprintf("args = [\"-y\", \"mcp-remote\", %q]\n", url)

	stripped := strings.TrimRight(string(stripTOMLTable(existing, table)), "\n")
	if stripped == "" {
		return []byte(block)
	}
	return []byte(stripped + "\n\n" + block)
}

// stripTOMLTable removes the [table] section and any [table.sub] subtables from
// TOML source: it drops each matching table header line and every line under it
// up to (but excluding) the next table header or end of input. Header matching is
// whitespace-tolerant and handles both [table] and [[table]] (array-of-tables)
// forms.
func stripTOMLTable(existing []byte, table string) []byte {
	lines := strings.Split(string(existing), "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			header := tomlHeaderName(trimmed)
			if header == table || strings.HasPrefix(header, table+".") {
				skipping = true
				continue
			}
			skipping = false
		}
		if skipping {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// tomlHeaderName extracts the dotted table name from a trimmed header line,
// tolerating both [name] and [[name]] and any surrounding spaces.
func tomlHeaderName(trimmed string) string {
	s := strings.TrimSpace(strings.TrimPrefix(trimmed, "[")) // "name]" or "name]]"
	s = strings.TrimSpace(strings.TrimSuffix(s, "]"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "]")) // second ] for [[name]]
	s = strings.TrimSpace(strings.TrimPrefix(s, "[")) // second [ for [[name]]
	return strings.TrimSpace(s)
}

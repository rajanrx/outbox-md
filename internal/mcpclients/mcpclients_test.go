package mcpclients

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

const testURL = "http://localhost:8181/mcp"

// memEnv builds an Env backed by an in-memory file map plus configurable command
// and directory probes, so tests never touch the real home dir or PATH.
type memEnv struct {
	home     string
	goos     string
	commands map[string]bool
	dirs     map[string]bool
	files    map[string][]byte
}

func newMemEnv() *memEnv {
	return &memEnv{
		home:     "/home/tester",
		goos:     "linux",
		commands: map[string]bool{},
		dirs:     map[string]bool{},
		files:    map[string][]byte{},
	}
}

func (m *memEnv) env() Env {
	return Env{
		HomeDir:       m.home,
		GOOS:          m.goos,
		CommandExists: func(name string) bool { return m.commands[name] },
		DirExists:     func(path string) bool { return m.dirs[path] },
		ReadFile: func(path string) ([]byte, error) {
			b, ok := m.files[path]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return b, nil
		},
		WriteFile: func(path string, data []byte, _ fs.FileMode) error {
			m.files[path] = append([]byte(nil), data...)
			return nil
		},
		MkdirAll:   func(string, fs.FileMode) error { return nil },
		RunCommand: func(string, []string) error { return nil },
	}
}

// --- MergeJSON ------------------------------------------------------------

func TestMergeJSONPreservesOtherServersAndKeys(t *testing.T) {
	existing := []byte(`{
  "theme": "dark",
  "mcpServers": {
    "other": { "url": "http://example/mcp" }
  }
}`)
	out, err := MergeJSON(existing, "outbox-md", map[string]any{"url": testURL})
	if err != nil {
		t.Fatal(err)
	}

	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if root["theme"] != "dark" {
		t.Errorf("top-level key not preserved: %v", root["theme"])
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers["other"] == nil {
		t.Error("existing server 'other' was dropped")
	}
	ob, _ := servers["outbox-md"].(map[string]any)
	if ob == nil || ob["url"] != testURL {
		t.Errorf("outbox-md entry missing/wrong: %v", servers["outbox-md"])
	}
}

func TestMergeJSONEmptyAndAbsentTreatedAsObject(t *testing.T) {
	for name, in := range map[string][]byte{
		"nil":        nil,
		"empty":      []byte(""),
		"whitespace": []byte("   \n\t "),
	} {
		out, err := MergeJSON(in, "outbox-md", map[string]any{"httpUrl": testURL})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var root map[string]any
		if err := json.Unmarshal(out, &root); err != nil {
			t.Fatalf("%s: invalid JSON: %v", name, err)
		}
		servers := root["mcpServers"].(map[string]any)
		ob := servers["outbox-md"].(map[string]any)
		if ob["httpUrl"] != testURL {
			t.Fatalf("%s: httpUrl not set: %v", name, ob)
		}
	}
}

func TestMergeJSONReplacesExistingOutboxEntry(t *testing.T) {
	existing := []byte(`{"mcpServers":{"outbox-md":{"url":"http://stale/mcp"}}}`)
	out, err := MergeJSON(existing, "outbox-md", map[string]any{"url": testURL})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "stale") {
		t.Fatalf("stale outbox-md entry not replaced:\n%s", out)
	}
}

func TestMergeJSONInvalidReturnsError(t *testing.T) {
	if _, err := MergeJSON([]byte("{not json"), "outbox-md", map[string]any{}); err == nil {
		t.Fatal("expected error for invalid JSON input")
	}
}

// --- MergeTOML -------------------------------------------------------------
//
// The Codex config merge is now backed by a real TOML parser, so every case
// asserts against the ROUND-TRIP-PARSED result (valid TOML, exactly one
// outbox-md bridge, other content preserved) rather than substring-matching the
// serialised text — the library chooses its own quoting/ordering.

// parseTOML fails the test unless b is valid TOML, and returns the decoded tree.
func parseTOML(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		t.Fatalf("output is not valid TOML: %v\n%s", err, b)
	}
	return m
}

// outboxServer extracts the mcp_servers.outbox-md table from a parsed config.
func outboxServer(t *testing.T, root map[string]any) map[string]any {
	t.Helper()
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers missing or not a table: %#v", root["mcp_servers"])
	}
	ob, ok := servers["outbox-md"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers.outbox-md missing or not a table: %#v", servers["outbox-md"])
	}
	return ob
}

// assertBridge asserts ob is exactly the mcp-remote stdio bridge for url.
func assertBridge(t *testing.T, ob map[string]any, url string) {
	t.Helper()
	if ob["command"] != "npx" {
		t.Errorf("command = %v, want npx", ob["command"])
	}
	args, ok := ob["args"].([]any)
	if !ok {
		t.Fatalf("args not an array: %#v", ob["args"])
	}
	want := []any{"-y", "mcp-remote", url}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %v, want %v", i, args[i], want[i])
		}
	}
}

func mergeTOML(t *testing.T, existing []byte, url string) []byte {
	t.Helper()
	out, err := MergeTOML(existing, "outbox-md", mcpRemoteBridge(url))
	if err != nil {
		t.Fatalf("MergeTOML errored on valid input: %v", err)
	}
	return out
}

func TestMergeTOMLIntoAbsentFile(t *testing.T) {
	for name, in := range map[string][]byte{"nil": nil, "empty": []byte(""), "whitespace": []byte("  \n\t ")} {
		out := mergeTOML(t, in, testURL)
		assertBridge(t, outboxServer(t, parseTOML(t, out)), testURL)
		if strings.Count(string(out), "[mcp_servers.outbox-md]") != 1 {
			t.Errorf("%s: want exactly one outbox-md table:\n%s", name, out)
		}
	}
}

func TestMergeTOMLPreservesUnrelatedTablesAndKeys(t *testing.T) {
	existing := []byte("model = \"gpt-5\"\ntemperature = 0.7\n\n" +
		"[mcp_servers.somethingelse]\ncommand = \"foo\"\nargs = [\"a\"]\n\n" +
		"[keep]\nnested = true\n")
	out := mergeTOML(t, existing, testURL)
	root := parseTOML(t, out)

	if root["model"] != "gpt-5" {
		t.Errorf("top-level string key lost: %#v", root["model"])
	}
	if root["temperature"] != 0.7 {
		t.Errorf("top-level float key lost: %#v", root["temperature"])
	}
	keep, _ := root["keep"].(map[string]any)
	if keep == nil || keep["nested"] != true {
		t.Errorf("[keep] table lost: %#v", root["keep"])
	}
	servers := root["mcp_servers"].(map[string]any)
	other, _ := servers["somethingelse"].(map[string]any)
	if other == nil || other["command"] != "foo" {
		t.Errorf("unrelated [mcp_servers.somethingelse] lost: %#v", servers["somethingelse"])
	}
	assertBridge(t, outboxServer(t, root), testURL)
}

// TestMergeTOMLMultilineStringLookalikeNotCorrupted is the catastrophic case the
// old line-based strip destroyed: a multi-line basic string whose body contains
// a line that LOOKS like the [mcp_servers.outbox-md] header. A real parser keeps
// the string intact and leaves the surrounding tables untouched.
func TestMergeTOMLMultilineStringLookalikeNotCorrupted(t *testing.T) {
	body := "You are helpful.\n[mcp_servers.outbox-md]\ncommand = \"evil\"\nkeep reading\n"
	existing := []byte("system_prompt = \"\"\"\n" + body + "\"\"\"\n\n[keep]\nk = 1\n")

	out := mergeTOML(t, existing, testURL)
	root := parseTOML(t, out) // must still be valid TOML

	if got, _ := root["system_prompt"].(string); got != body {
		t.Errorf("multi-line string corrupted:\ngot  %q\nwant %q", got, body)
	}
	keep, _ := root["keep"].(map[string]any)
	if keep == nil || keep["k"] != int64(1) {
		t.Errorf("[keep] table after the multi-line string was lost: %#v", root["keep"])
	}
	assertBridge(t, outboxServer(t, root), testURL)
}

// TestMergeTOMLReplacesExistingEntryAllForms covers the forms the old strip
// failed to recognise (so it appended a duplicate → invalid TOML): trailing
// header comment, inline-table value, and dotted-key. Each must be replaced
// cleanly, leaving exactly one outbox-md bridge and no stale value.
func TestMergeTOMLReplacesExistingEntryAllForms(t *testing.T) {
	cases := map[string][]byte{
		"trailing-header-comment": []byte("[mcp_servers.outbox-md] # pre-existing note\ncommand = \"old\"\nargs = [\"stale\"]\n"),
		"inline-table":            []byte("[mcp_servers]\noutbox-md = { command = \"old\", args = [\"stale\"] }\n"),
		"dotted-key":              []byte("mcp_servers.outbox-md.command = \"old\"\nmcp_servers.outbox-md.args = [\"stale\"]\n"),
	}
	for name, existing := range cases {
		t.Run(name, func(t *testing.T) {
			out := mergeTOML(t, existing, testURL)
			if strings.Contains(string(out), "stale") {
				t.Fatalf("stale value not replaced:\n%s", out)
			}
			if n := strings.Count(string(out), "[mcp_servers.outbox-md]"); n != 1 {
				t.Fatalf("want exactly one outbox-md table, got %d:\n%s", n, out)
			}
			assertBridge(t, outboxServer(t, parseTOML(t, out)), testURL)
		})
	}
}

func TestMergeTOMLIsIdempotent(t *testing.T) {
	existing := []byte("model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"foo\"\n")
	first := mergeTOML(t, existing, testURL)
	second := mergeTOML(t, first, testURL)
	if !bytes.Equal(first, second) {
		t.Fatalf("output not stable across re-runs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestMergeTOMLMalformedReturnsError(t *testing.T) {
	if _, err := MergeTOML([]byte("this = = broken\n[unterminated\n"), "outbox-md", mcpRemoteBridge(testURL)); err == nil {
		t.Fatal("expected error for malformed TOML input")
	}
}

// --- detection ------------------------------------------------------------

func TestDetectionViaProbes(t *testing.T) {
	m := newMemEnv()
	m.commands["claude"] = true
	m.commands["gemini"] = true
	m.commands["codex"] = false
	m.dirs[filepath.Join(m.home, ".cursor")] = true
	// windsurf + claude-desktop dirs absent
	env := m.env()

	want := map[string]bool{
		"claude-code":    true,
		"gemini":         true,
		"cursor":         true,
		"windsurf":       false,
		"claude-desktop": false,
		"codex":          false,
	}
	for _, c := range Clients() {
		if got := c.Detect(env); got != want[c.Slug] {
			t.Errorf("%s detect = %v, want %v", c.Slug, got, want[c.Slug])
		}
	}
}

func TestClaudeDesktopDirIsOSSpecific(t *testing.T) {
	mac := newMemEnv()
	mac.goos = "darwin"
	if got := claudeDesktopDir(mac.env()); got != filepath.Join(mac.home, "Library", "Application Support", "Claude") {
		t.Errorf("darwin path wrong: %s", got)
	}
	lin := newMemEnv()
	lin.goos = "linux"
	if got := claudeDesktopDir(lin.env()); got != filepath.Join(lin.home, ".config", "Claude") {
		t.Errorf("linux path wrong: %s", got)
	}
}

// --- Register orchestration ----------------------------------------------

func TestRegisterAllAbsentSkipsNeverErrors(t *testing.T) {
	m := newMemEnv() // nothing installed
	results, err := Register(m.env(), testURL, Options{})
	if err != nil {
		t.Fatalf("Register errored on all-absent: %v", err)
	}
	if len(results) != len(Clients()) {
		t.Fatalf("want %d results, got %d", len(Clients()), len(results))
	}
	for _, r := range results {
		if r.Action != ActionSkipped {
			t.Errorf("%s: want skipped, got %s", r.Slug, r.Action)
		}
	}
	if len(m.files) != 0 {
		t.Fatalf("no config should be written when nothing detected: %v", m.files)
	}
}

func TestRegisterDetectedWiresCorrectShapes(t *testing.T) {
	m := newMemEnv()
	m.dirs[filepath.Join(m.home, ".cursor")] = true
	m.dirs[filepath.Join(m.home, ".codeium", "windsurf")] = true
	m.dirs[claudeDesktopDir(m.env())] = true
	m.commands["gemini"] = true
	m.commands["codex"] = true

	if _, err := Register(m.env(), testURL, Options{}); err != nil {
		t.Fatal(err)
	}

	// Cursor: {"url": ...}
	assertJSONServer(t, m.files[cursorPath(m.env())], "url", testURL)
	// Windsurf: {"serverUrl": ...}
	assertJSONServer(t, m.files[windsurfPath(m.env())], "serverUrl", testURL)
	// Gemini: {"httpUrl": ...}
	assertJSONServer(t, m.files[geminiPath(m.env())], "httpUrl", testURL)
	// Claude Desktop: mcp-remote bridge
	assertJSONServer(t, m.files[claudeDesktopPath(m.env())], "command", "npx")
	// Codex: TOML mcp-remote bridge — round-trip parse and assert the shape.
	assertBridge(t, outboxServer(t, parseTOML(t, m.files[codexPath(m.env())])), testURL)
}

// TestCodexRegisterFailsSafeOnUnparseableConfig proves the fail-safe path: an
// existing but invalid config.toml is NEVER overwritten; the client is reported
// ActionFailed with the manual [mcp_servers.outbox-md] snippet, and the file on
// disk is left byte-for-byte unchanged.
func TestCodexRegisterFailsSafeOnUnparseableConfig(t *testing.T) {
	m := newMemEnv()
	m.commands["codex"] = true
	bad := []byte("model = \"gpt-5\"\nthis = = not valid toml\n[unterminated\n")
	m.files[codexPath(m.env())] = append([]byte(nil), bad...)

	results, err := Register(m.env(), testURL, Options{Only: []string{"codex"}})
	if err != nil {
		t.Fatalf("Register returned an orchestration error: %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionFailed {
		t.Fatalf("want a single ActionFailed result, got %#v", results)
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "[mcp_servers.outbox-md]") {
		t.Errorf("failure must carry the manual snippet, got: %v", results[0].Err)
	}
	if got := m.files[codexPath(m.env())]; !bytes.Equal(got, bad) {
		t.Errorf("unparseable config must be left untouched, got:\n%s", got)
	}
}

func TestRegisterAllForcesWritesEvenWhenAbsent(t *testing.T) {
	m := newMemEnv() // nothing installed
	results, err := Register(m.env(), testURL, Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	// The five file-based clients must have written configs despite being absent.
	for _, p := range []string{cursorPath(m.env()), windsurfPath(m.env()), geminiPath(m.env()), claudeDesktopPath(m.env()), codexPath(m.env())} {
		if _, ok := m.files[p]; !ok {
			t.Errorf("--all did not write %s", p)
		}
	}
	// Claude Code has no file; absent CLI → a note carrying the manual command.
	var cc Result
	for _, r := range results {
		if r.Slug == "claude-code" {
			cc = r
		}
	}
	if cc.Action != ActionNoted || !strings.Contains(cc.Note, "claude mcp add --transport http outbox-md "+testURL) {
		t.Errorf("claude-code under --all should note the manual command, got %+v", cc)
	}
}

func TestRegisterClaudeCodeRunsCommandWhenPresent(t *testing.T) {
	m := newMemEnv()
	m.commands["claude"] = true
	var ran [][]string
	e := m.env()
	e.RunCommand = func(name string, args []string) error {
		ran = append(ran, append([]string{name}, args...))
		return nil
	}
	results, err := Register(e, testURL, Options{Only: []string{"claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0][0] != "claude" || ran[0][1] != "mcp" {
		t.Fatalf("claude command not run correctly: %v", ran)
	}
	if results[0].Action != ActionWired {
		t.Errorf("want wired, got %s", results[0].Action)
	}
}

func TestRegisterClaudeCodeCommandFailureIsSoftNote(t *testing.T) {
	m := newMemEnv()
	m.commands["claude"] = true
	e := m.env()
	e.RunCommand = func(string, []string) error { return os.ErrPermission }
	results, err := Register(e, testURL, Options{Only: []string{"claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Action != ActionNoted {
		t.Fatalf("command failure should be a soft note, got %s", results[0].Action)
	}
}

func TestRegisterOnlyUnknownErrors(t *testing.T) {
	m := newMemEnv()
	if _, err := Register(m.env(), testURL, Options{Only: []string{"nope"}}); err == nil {
		t.Fatal("expected error for unknown client slug")
	}
}

func TestRegisterOnlyForcesUndetected(t *testing.T) {
	m := newMemEnv()                                                              // cursor not installed
	results, err := Register(m.env(), testURL, Options{Only: []string{"CURSOR"}}) // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != ActionWired {
		t.Fatalf("--client cursor should force-wire, got %+v", results)
	}
	if _, ok := m.files[cursorPath(m.env())]; !ok {
		t.Error("cursor config not written under --client")
	}
}

func assertJSONServer(t *testing.T, data []byte, key, want string) {
	t.Helper()
	if data == nil {
		t.Fatalf("no config written")
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	ob, _ := servers["outbox-md"].(map[string]any)
	if ob == nil {
		t.Fatalf("no outbox-md server in:\n%s", data)
	}
	if got, _ := ob[key].(string); got != want {
		t.Fatalf("key %q = %q, want %q\n%s", key, got, want, data)
	}
}

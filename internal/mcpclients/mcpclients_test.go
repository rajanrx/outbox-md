package mcpclients

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// --- MergeTOML ------------------------------------------------------------

func TestMergeTOMLIntoAbsentFile(t *testing.T) {
	out := string(MergeTOML(nil, "outbox-md", testURL))
	if !strings.Contains(out, "[mcp_servers.outbox-md]") {
		t.Fatalf("missing table header:\n%s", out)
	}
	if !strings.Contains(out, `command = "npx"`) || !strings.Contains(out, `"mcp-remote", "`+testURL+`"`) {
		t.Fatalf("missing mcp-remote bridge:\n%s", out)
	}
}

func TestMergeTOMLPreservesOtherTables(t *testing.T) {
	existing := []byte("model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"foo\"\nargs = [\"a\"]\n")
	out := string(MergeTOML(existing, "outbox-md", testURL))
	if !strings.Contains(out, `model = "gpt-5"`) {
		t.Errorf("top-level key lost:\n%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.other]") || !strings.Contains(out, `command = "foo"`) {
		t.Errorf("unrelated table lost:\n%s", out)
	}
	if !strings.Contains(out, "[mcp_servers.outbox-md]") {
		t.Errorf("outbox-md table not added:\n%s", out)
	}
}

func TestMergeTOMLReplacesNotDuplicates(t *testing.T) {
	first := MergeTOML(nil, "outbox-md", "http://old/mcp")
	second := string(MergeTOML(first, "outbox-md", testURL))
	if strings.Count(second, "[mcp_servers.outbox-md]") != 1 {
		t.Fatalf("table duplicated on re-run:\n%s", second)
	}
	if strings.Contains(second, "http://old/mcp") {
		t.Fatalf("stale url not replaced:\n%s", second)
	}
}

func TestMergeTOMLStripsSubtables(t *testing.T) {
	existing := []byte("[mcp_servers.outbox-md]\ncommand = \"npx\"\n\n[mcp_servers.outbox-md.env]\nFOO = \"bar\"\n\n[other]\nk = 1\n")
	out := string(MergeTOML(existing, "outbox-md", testURL))
	if strings.Contains(out, "[mcp_servers.outbox-md.env]") {
		t.Errorf("stale subtable not stripped:\n%s", out)
	}
	if !strings.Contains(out, "[other]") || !strings.Contains(out, "k = 1") {
		t.Errorf("unrelated table after subtable was lost:\n%s", out)
	}
	if strings.Count(out, "[mcp_servers.outbox-md]") != 1 {
		t.Errorf("expected exactly one outbox-md table:\n%s", out)
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
	// Codex: TOML bridge
	toml := string(m.files[codexPath(m.env())])
	if !strings.Contains(toml, "[mcp_servers.outbox-md]") || !strings.Contains(toml, "mcp-remote") {
		t.Errorf("codex toml wrong:\n%s", toml)
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

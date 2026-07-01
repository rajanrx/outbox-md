package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/registry"
	"gopkg.in/yaml.v3"
)

// pathsCmd prints the resolved on-disk locations outbox uses, labelled and one
// per line, so a user can cd/open them. It is mode-aware: in multi-project mode
// (any project registered) the review database lives next to the registry under
// the config home and each registered project has its own outbox.yaml; in
// single-dir mode the database is ./.outbox/outbox.db and the config is
// ./outbox.yaml (honouring OUTBOX_DIR, matching what `outbox up` would serve).
func pathsCmd(out io.Writer) error {
	reg := registryPath()
	fmt.Fprintf(out, "registry (projects.json):  %s\n", reg)

	projects, err := registry.Load(reg)
	if err != nil {
		return err
	}
	if len(projects) > 0 {
		fmt.Fprintf(out, "review database:           %s\n", filepath.Join(configHomeDir(), "outbox.db"))
		fmt.Fprintln(out, "mode:                      multi-project")
		for _, p := range projects {
			label := "config (" + p.Name + "):"
			fmt.Fprintf(out, "%s%s%s\n", label, pad(len(label)), filepath.Join(p.Root, "outbox.yaml"))
		}
		return nil
	}

	dir := getenv("OUTBOX_DIR", ".")
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	fmt.Fprintf(out, "review database:           %s\n", filepath.Join(dir, ".outbox", "outbox.db"))
	fmt.Fprintf(out, "config (outbox.yaml):      %s\n", filepath.Join(dir, "outbox.yaml"))
	fmt.Fprintln(out, "mode:                      single-dir")
	return nil
}

// pad returns spaces to align a value under the fixed label column (27 chars)
// used by pathsCmd; it never returns a negative-width string.
func pad(used int) string {
	const col = 27
	if used >= col {
		return " "
	}
	return strings.Repeat(" ", col-used)
}

// settingsField is one structured (typed) field of outbox.yaml the settings
// command can tune. Only bool fields exist today (auto_update, auto_reply); the
// free-text fields (agent_cmd, sources) are shown read-only and never prompted.
type settingsField struct {
	key  string // the outbox.yaml key
	kind string // "bool"
	desc string // short human description
	cur  func(config.Config) string
}

// structuredFields is the ordered set of tunable outbox.yaml fields.
var structuredFields = []settingsField{
	{key: "auto_update", kind: "bool", desc: "self-update on `outbox up`", cur: func(c config.Config) string { return boolStr(c.AutoUpdate) }},
	{key: "auto_reply", kind: "bool", desc: "spawn the agent CLI on each human comment", cur: func(c config.Config) string { return boolStr(c.AutoReply) }},
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// parseBoolLoose accepts the forms the settings command allows for a bool field:
// true/false, 1/0, yes/no (and on/off). It returns the parsed value and whether
// the input was recognised.
func parseBoolLoose(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
}

// settingsCmd tunes the structured fields of ./outbox.yaml. With no args it runs
// an interactive walkthrough (Enter keeps the current value); with <key> <value>
// it sets one field directly. It operates on ./outbox.yaml in the current
// directory and never creates it — an absent file tells the user to run
// `outbox init` first. Unmanaged keys and comments are preserved by editing the
// parsed YAML node tree rather than re-marshalling a struct. When stdin is not a
// terminal (e.g. piped from /dev/null), the interactive form prints the current
// settings and exits rather than hanging on input.
func settingsCmd(args []string, out io.Writer, stdin io.Reader) error {
	path := "outbox.yaml"
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no outbox.yaml in the current directory — run \"outbox init\" first")
		}
		return err
	}

	// Direct set: `outbox settings <key> <value>`.
	if len(args) >= 2 {
		return settingsSet(path, args[0], strings.Join(args[1:], " "), out)
	}
	if len(args) == 1 {
		usageFor(out, "settings")
		return fmt.Errorf("settings <key> <value> requires a value (or run \"outbox settings\" with no args for the interactive form)")
	}

	// Interactive form. Guard a non-TTY stdin: print current settings and exit 0
	// rather than block on a read that will never receive a line.
	if !isTerminal(stdin) {
		printSettings(out)
		return nil
	}
	return settingsInteractive(path, out, stdin)
}

// isTerminal reports whether r is an interactive terminal. Only an *os.File
// backed by a real tty qualifies (via an isatty ioctl, so /dev/null — a
// character device — correctly reads as NOT a terminal); a pipe, a regular file,
// /dev/null, or a non-file reader (e.g. a test buffer) is treated as
// non-interactive so the interactive walkthrough never blocks on input.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// printSettings writes the current effective settings (structured + read-only
// free-text) without prompting — used for the non-TTY interactive fallback.
func printSettings(out io.Writer) {
	cfg := config.Load(".")
	fmt.Fprintln(out, "current settings (./outbox.yaml):")
	for _, f := range structuredFields {
		fmt.Fprintf(out, "  %-12s %-7s (%s) — %s\n", f.key, f.cur(cfg), f.kind, f.desc)
	}
	fmt.Fprintf(out, "  %-12s %-7s (read-only — edit in outbox.yaml)\n", "agent_cmd", trunc(cfg.AgentCmd, 40))
	fmt.Fprintf(out, "  %-12s %-7s (read-only — edit in outbox.yaml)\n", "sources", trunc(strings.Join(cfg.Sources, ", "), 40))
}

func trunc(s string, n int) string {
	if s == "" {
		return "(unset)"
	}
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// settingsSet validates and writes a single structured field, preserving the
// rest of the file. An unknown key is rejected with the list of valid keys.
func settingsSet(path, key, value string, out io.Writer) error {
	f, ok := fieldByKey(key)
	if !ok {
		return fmt.Errorf("unknown setting %q — valid keys: %s", key, strings.Join(structuredKeys(), ", "))
	}
	norm, err := validateField(f, value)
	if err != nil {
		return err
	}
	if err := writeYAMLKey(path, key, norm); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ %s = %s\n", key, norm)
	return nil
}

// settingsInteractive walks each structured field, showing its current value and
// reading a line: Enter keeps the current value; any other input is validated and
// re-prompts on error. Free-text fields are shown read-only. The file is written
// once at the end.
func settingsInteractive(path string, out io.Writer, stdin io.Reader) error {
	cfg := config.Load(".")
	sc := bufio.NewScanner(stdin)
	updates := map[string]string{}

	fmt.Fprintln(out, "outbox settings — press Enter to keep the current value.")
	for _, f := range structuredFields {
		cur := f.cur(cfg)
		for {
			fmt.Fprintf(out, "%s  [%s]  (%s): ", f.key, cur, f.kind)
			if !sc.Scan() {
				// Input ended mid-walkthrough: stop prompting, keep what we have.
				goto done
			}
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				break // keep current
			}
			norm, err := validateField(f, line)
			if err != nil {
				fmt.Fprintf(out, "  invalid: %v\n", err)
				continue
			}
			updates[f.key] = norm
			break
		}
	}
done:
	// Show the read-only free-text fields so the user knows they exist.
	fmt.Fprintf(out, "agent_cmd  [%s]  (read-only — edit in outbox.yaml)\n", trunc(cfg.AgentCmd, 50))
	fmt.Fprintf(out, "sources    [%s]  (read-only — edit in outbox.yaml)\n", trunc(strings.Join(cfg.Sources, ", "), 50))

	for k, v := range updates {
		if err := writeYAMLKey(path, k, v); err != nil {
			return err
		}
	}
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	fmt.Fprintf(out, "✓ wrote %s\n", abs)
	return nil
}

func fieldByKey(key string) (settingsField, bool) {
	for _, f := range structuredFields {
		if f.key == key {
			return f, true
		}
	}
	return settingsField{}, false
}

func structuredKeys() []string {
	keys := make([]string, len(structuredFields))
	for i, f := range structuredFields {
		keys[i] = f.key
	}
	return keys
}

// validateField validates a raw value against a field's kind and returns the
// normalised string to store (e.g. "true"/"false" for a bool).
func validateField(f settingsField, raw string) (string, error) {
	switch f.kind {
	case "bool":
		b, ok := parseBoolLoose(raw)
		if !ok {
			return "", fmt.Errorf("%s expects a boolean (true/false, 1/0, yes/no)", f.key)
		}
		return boolStr(b), nil
	default:
		return "", fmt.Errorf("unsupported field kind %q", f.kind)
	}
}

// writeYAMLKey sets key=value in the root mapping of the YAML file at path,
// preserving every other key (and, where yaml.v3 allows, surrounding comments) by
// editing the parsed node tree in place rather than re-serialising a struct. A
// missing key is appended; an existing scalar value is updated. The value is
// written as a bool node (unquoted true/false).
func writeYAMLKey(path, key, value string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("outbox.yaml: %w", err)
	}
	root := documentRoot(&doc)
	if root == nil {
		// Empty or comments-only file: build a fresh mapping root.
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	}
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("outbox.yaml: top-level YAML is not a mapping")
	}
	setScalarBool(root, key, value)

	b, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// documentRoot returns the root mapping node of a parsed YAML document (the first
// content node of a DocumentNode), or nil when the document is empty.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

// setScalarBool sets key to a bool scalar (value must be "true"/"false") in the
// mapping node, updating an existing entry or appending a new key/value pair.
func setScalarBool(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			v := mapping.Content[i+1]
			v.Kind = yaml.ScalarNode
			v.Tag = "!!bool"
			v.Value = value
			v.Style = 0
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value},
	)
}

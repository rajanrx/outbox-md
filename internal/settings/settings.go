// Package settings owns the comment-preserving write path for the structured
// fields of an outbox.yaml. It is the SINGLE place that validates a field value
// and edits the file's node tree in place, so both write surfaces — the CLI
// `outbox settings` command and the HTTP PUT /api/settings endpoint — round-trip
// identically: unmanaged keys (sources, webhook, …) and surrounding comments are
// preserved, and a value is written with the correct YAML scalar tag/quoting for
// its type.
package settings

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind is the type of a structured outbox.yaml field, deciding both how a raw
// value is validated and which YAML scalar tag it is written with.
type Kind string

const (
	KindBool   Kind = "bool"
	KindString Kind = "string"
	KindInt    Kind = "int"
)

// Field is one editable top-level outbox.yaml key.
type Field struct {
	Key  string
	Kind Kind
	Desc string
}

// Editable is the ordered set of top-level scalar outbox.yaml fields the write
// surfaces can tune. They are all top-level scalars so the comment-preserving
// node-tree write applies cleanly; the nested `agent.batch_size` guardrail is
// intentionally NOT here (writing a nested key is out of scope for this path).
var Editable = []Field{
	{Key: "auto_update", Kind: KindBool, Desc: "self-update on `outbox up`"},
	{Key: "auto_reply", Kind: KindBool, Desc: "spawn the agent CLI on each human comment"},
	{Key: "agent_cmd", Kind: KindString, Desc: "command template the auto-reply engine spawns ({prompt} token)"},
	{Key: "council_rounds", Kind: KindInt, Desc: "max council discussion rounds (early-exit on consensus)"},
	{Key: "council_budget", Kind: KindInt, Desc: "per-council token budget guardrail"},
	{Key: "council_deadlock_threshold", Kind: KindInt, Desc: "confidence % below which the chair posts \"no consensus\" options"},
}

// FieldByKey returns the editable field for key, if any.
func FieldByKey(key string) (Field, bool) {
	for _, f := range Editable {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// Keys returns the editable field keys, for error messages.
func Keys() []string {
	out := make([]string, len(Editable))
	for i, f := range Editable {
		out[i] = f.Key
	}
	return out
}

// Validate normalises raw against kind, returning the canonical string to store
// (true/false for a bool; the canonical integer for an int; the trimmed string
// for a string) or an error describing the expected form.
func Validate(kind Kind, key, raw string) (string, error) {
	switch kind {
	case KindBool:
		b, ok := ParseBoolLoose(raw)
		if !ok {
			return "", fmt.Errorf("%s expects a boolean (true/false, 1/0, yes/no)", key)
		}
		if b {
			return "true", nil
		}
		return "false", nil
	case KindInt:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("%s expects an integer", key)
		}
		return strconv.Itoa(n), nil
	case KindString:
		return strings.TrimSpace(raw), nil
	default:
		return "", fmt.Errorf("unsupported field kind %q", kind)
	}
}

// ParseBoolLoose accepts the forms the settings surfaces allow for a bool field:
// true/false, 1/0, yes/no, on/off. It returns the parsed value and whether the
// input was recognised.
func ParseBoolLoose(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
}

// WriteKey sets key to value (typed by kind) in the root mapping of the YAML
// file at path, preserving every other key and (where yaml.v3 allows) surrounding
// comments by editing the parsed node tree in place rather than re-serialising a
// struct. A missing key is appended; an existing scalar value is updated. value
// MUST already be normalised by Validate for the kind.
func WriteKey(path, key, value string, kind Kind) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("outbox.yaml: %w", err)
	}
	root := documentRoot(&doc)
	// A brand-new `outbox init` file is ALL comments — it unmarshals to an empty
	// document or a null scalar, not a mapping (yaml.v3 doesn't surface the
	// standalone comments on a re-marshallable node, so rebuilding the tree would
	// wipe the starter guidance). For that nullish case, APPEND the key to the raw
	// bytes: every original comment is preserved verbatim and the key is added.
	// Only a genuinely non-mapping top level (e.g. a top-level list) is an error.
	if !isMapping(root) {
		if root != nil && !isNullish(root) {
			return fmt.Errorf("outbox.yaml: top-level YAML is not a mapping")
		}
		// Marshal a one-key mapping so the value is correctly tagged and quoted
		// (a string like the agent command carries {, *, spaces), then append the
		// resulting line(s) after the preserved original bytes.
		frag, err := yaml.Marshal(map[string]*yaml.Node{key: scalarNode(value, kind)})
		if err != nil {
			return err
		}
		out := data
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, frag...)
		return os.WriteFile(path, out, 0o644)
	}
	setScalar(root, key, value, kind)

	b, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// tagFor maps a field kind to its YAML scalar tag.
func tagFor(kind Kind) string {
	switch kind {
	case KindBool:
		return "!!bool"
	case KindInt:
		return "!!int"
	default:
		return "!!str"
	}
}

// scalarNode builds a scalar node for value with the tag for kind. Style 0 lets
// yaml.v3 auto-quote a string when needed (and leave bools/ints bare).
func scalarNode(value string, kind Kind) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagFor(kind), Value: value}
}

// documentRoot returns the root node of a parsed YAML document (the first content
// node of a DocumentNode), or nil when the document has no content.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

// isMapping reports whether n is a usable mapping node.
func isMapping(n *yaml.Node) bool { return n != nil && n.Kind == yaml.MappingNode }

// isNullish reports whether n represents "no content" — a zero node or a null /
// empty scalar (what a comment-only outbox.yaml unmarshals to). Such a root is
// safe to append to rather than erroring.
func isNullish(n *yaml.Node) bool {
	if n == nil || n.Kind == 0 {
		return true
	}
	if n.Kind == yaml.ScalarNode {
		v := strings.TrimSpace(n.Value)
		return n.Tag == "!!null" || v == "" || v == "null" || v == "~"
	}
	return false
}

// setScalar sets key to a scalar of the given kind in the mapping node, updating
// an existing entry (retagging it) or appending a new key/value pair.
func setScalar(mapping *yaml.Node, key, value string, kind Kind) {
	tag := tagFor(kind)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			v := mapping.Content[i+1]
			v.Kind = yaml.ScalarNode
			v.Tag = tag
			v.Value = value
			v.Style = 0
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		scalarNode(value, kind),
	)
}

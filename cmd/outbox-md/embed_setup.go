package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/settings"
)

// embedCmd dispatches the `outbox embed …` subcommand group: run (the embedder
// bridge embed_cmd points at), setup (the one-shot enabler), and status. Bare
// `outbox embed` prints the group help. It is wired into run()'s switch.
func embedCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		usageFor(out, "embed")
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "run":
		return embedRun(rest, os.Stdin, out, os.Stderr)
	case "setup":
		return embedSetup(rest, out)
	case "status":
		return embedStatus(rest, out)
	case "help", "-h", "--help":
		usageFor(out, "embed")
		return nil
	default:
		usageFor(out, "embed")
		return fmt.Errorf("unknown embed subcommand %q (run \"outbox help embed\")", sub)
	}
}

// embedSetup is the one-shot enabler: it verifies ollama is installed + reachable,
// pulls the embedding model (streaming progress), and points embed_cmd at THIS
// binary's `embed run` — so a single command turns semantic RAG on.
func embedSetup(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("embed setup", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { usageFor(out, "embed") }
	model := fs.String("model", defaultEmbedModel, "ollama embedding model to pull + configure")
	url := fs.String("url", defaultOllamaURL, "ollama base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 1. ollama on PATH? Never install system tools — print a hint and STOP.
	if _, err := lookPath("ollama"); err != nil {
		fmt.Fprintln(out, "ollama is not installed — it powers local, no-cost embeddings.")
		fmt.Fprintln(out, "  install it:  brew install ollama   (or see https://ollama.com/download)")
		fmt.Fprintln(out, "then re-run:  outbox embed setup")
		return nil
	}

	// 2. Daemon reachable? A quick GET to the base URL. STOP with a hint if not.
	if !ollamaReachable(*url) {
		fmt.Fprintf(out, "ollama is installed but not reachable at %s.\n", *url)
		fmt.Fprintln(out, "  start it:  ollama serve   (or launch the ollama app)")
		fmt.Fprintln(out, "then re-run:  outbox embed setup")
		return nil
	}

	// 3. Resolve (and, if absent, create) the outbox.yaml we will write BEFORE the
	// pull, so a missing/unwritable config fails fast rather than after a multi-GB
	// download.
	cfgPath := resolvedConfigPath()
	if err := ensureConfigFile(cfgPath); err != nil {
		return err
	}

	// 4. Pull the model, streaming ollama's own progress output to the user.
	fmt.Fprintf(out, "pulling ollama model %q …\n", *model)
	pull := exec.Command("ollama", "pull", *model)
	pull.Stdout, pull.Stderr = out, out
	if err := pull.Run(); err != nil {
		return fmt.Errorf("embed setup: `ollama pull %s` failed: %w", *model, err)
	}

	// 5. Point embed_cmd at this binary's `embed run`. os.Executable() so it resolves
	// off PATH; --url only when non-default (keeps the command clean for the common
	// local case).
	bin, err := os.Executable()
	if err != nil || strings.TrimSpace(bin) == "" {
		bin = "outbox" // last-resort fallback: rely on PATH
	}
	command := embedCmdString(bin, *model, *url)
	if err := settings.WriteKey(cfgPath, "embed_cmd", command, settings.KindString); err != nil {
		return err
	}

	fmt.Fprintf(out, "\n✓ set embed_cmd in %s\n", cfgPath)
	fmt.Fprintf(out, "    %s\n", command)
	fmt.Fprintln(out, "restart `outbox up` to apply — the retrieval chip will go semantic.")
	return nil
}

// embedCmdString builds the embed_cmd value that points at this binary's embed
// bridge. The --url flag is included only when it differs from the default, so the
// common local setup stays "<bin> embed run --model <model>". argv is whitespace-
// split by CmdEmbedder (no shell), matching the agent_cmd contract.
func embedCmdString(bin, model, url string) string {
	cmd := fmt.Sprintf("%s embed run --model %s", bin, model)
	if u := strings.TrimSpace(url); u != "" && u != defaultOllamaURL {
		cmd += " --url " + u
	}
	return cmd
}

// embedStatus reports whether semantic RAG is ready: is ollama installed +
// reachable, is the model pulled, and is embed_cmd currently configured. It prints
// a guidance line pointing at `outbox embed setup` when retrieval is not wired.
func embedStatus(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("embed status", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { usageFor(out, "embed") }
	model := fs.String("model", defaultEmbedModel, "ollama embedding model to check for")
	url := fs.String("url", defaultOllamaURL, "ollama base URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	installed := false
	if _, err := lookPath("ollama"); err == nil {
		installed = true
	}
	reachable := installed && ollamaReachable(*url)

	fmt.Fprintf(out, "ollama installed:  %s\n", yesNo(installed))
	fmt.Fprintf(out, "ollama reachable:  %s (%s)\n", yesNo(reachable), *url)

	pulled := false
	if installed {
		pulled = ollamaHasModel(*model)
	}
	fmt.Fprintf(out, "model %q pulled:   %s\n", *model, yesNo(pulled))

	cfg := config.Load(getenv("OUTBOX_DIR", "."))
	embed := strings.TrimSpace(cfg.EmbedCmd)
	if embed == "" {
		fmt.Fprintln(out, "embed_cmd:         (unset — retrieval is keyword-only over FTS5)")
	} else {
		fmt.Fprintf(out, "embed_cmd:         %s\n", embed)
	}

	if embed == "" || !installed || !reachable || !pulled {
		fmt.Fprintln(out, "\nRun `outbox embed setup` to enable semantic search (ollama + embeddinggemma).")
	} else {
		fmt.Fprintln(out, "\nSemantic RAG is configured. Restart `outbox up` if you just enabled it.")
	}
	return nil
}

// ollamaReachable reports whether the ollama daemon answers a quick GET at base. Any
// HTTP response (2xx/3xx/4xx) counts as reachable (the daemon is up); only a
// transport error (nothing listening) counts as down. The check is short-timeout so
// setup/status never hang on a wedged socket.
func ollamaReachable(baseURL string) bool {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = defaultOllamaURL
	}
	hc := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, base+"/api/version", nil)
	if err != nil {
		return false
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// ollamaHasModel reports whether `ollama list` includes model (matched on the name
// prefix, so "embeddinggemma" matches the "embeddinggemma:latest" tag). Any error
// running ollama list means we cannot confirm the model → false.
func ollamaHasModel(model string) bool {
	out, err := exec.Command("ollama", "list").Output()
	if err != nil {
		return false
	}
	want := strings.TrimSpace(model)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if name == want || strings.HasPrefix(name, want+":") {
			return true
		}
	}
	return false
}

// resolvedConfigPath returns the outbox.yaml `outbox embed setup` writes, resolved
// the same way `outbox paths` reports the single-dir config: OUTBOX_DIR (default
// ".") joined with outbox.yaml.
func resolvedConfigPath() string {
	return filepath.Join(getenv("OUTBOX_DIR", "."), "outbox.yaml")
}

// ensureConfigFile makes sure path exists so the comment-preserving settings write
// (which reads the file first) has something to edit. A missing file is seeded with
// the starter config, so `outbox embed setup` works as a true one-liner without a
// prior `outbox init`.
func ensureConfigFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(starterConfig), 0o644)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajanrx/outbox-md/internal/api"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/mcp"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
	"github.com/rajanrx/outbox-md/internal/webhook"
	"github.com/rajanrx/outbox-md/web"
)

// newMux is the minimal handler used by the health-check test.
func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// safeJoin resolves path under dir and refuses any result that escapes dir
// (defense-in-depth against path traversal on the file-write path).
func safeJoin(dir, path string) (string, error) {
	target := filepath.Join(dir, path)
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing to write outside managed dir: %q", path)
	}
	return target, nil
}

// atomicWrite writes content to a temp file in the same directory and renames
// it into place, so a failed or partial write never corrupts the target file.
// It preserves the target's permission bits, and best-effort its ownership, so
// the rename does not silently change mode (CreateTemp defaults to 0600) or
// leave a root-owned file on a Docker bind mount.
func atomicWrite(target, content string) error {
	mode := os.FileMode(0o644)
	uid, gid := -1, -1
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".outbox-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if uid >= 0 {
		// Best-effort: only succeeds when permitted (e.g. running as root in
		// Docker). A failure here must not block the write.
		_ = os.Chown(tmpName, uid, gid)
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return err
	}
	return nil
}

// ensureDataDir verifies the data path is a directory (creating it if absent),
// and fails with a clear message if it points at a file — the most common
// mistake (mounting a single .md instead of a folder).
func ensureDataDir(dir string) error {
	fi, err := os.Stat(dir)
	if err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("data path %q is a file, not a directory — mount a folder of .md files "+
				"(e.g. -v \"$PWD/specs:/data\"), not a single file", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// importMarkdown ingests .md files under dir. When sources is empty it walks the
// whole dir (the default, backward-compatible behaviour). When non-empty, each
// entry is a folder path OR a glob relative to dir: a folder is walked
// recursively, a glob is expanded, and every matched .md is imported keyed by its
// path relative to dir (so paths stay project-relative and disambiguate across
// folders). Entries that escape dir are rejected; overlapping matches are
// de-duped so a file is never imported twice.
func importMarkdown(st *store.Store, dir string, sources []string) error {
	if len(sources) == 0 {
		return importTree(st, dir, dir)
	}
	seen := map[string]bool{}
	for _, src := range sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		// A plain entry names a folder served recursively (or an exact file); a
		// glob entry matches files single-level. This mirrors config.Config.Serves
		// exactly, so a glob like "docs/*" never imports a nested file it would
		// then hide at serve time — use a plain folder entry to recurse.
		isGlob := strings.ContainsAny(src, "*?[")
		// Resolve within dir and refuse anything that escapes it. safeJoin cleans
		// the path and rejects traversal; the glob metacharacters survive Join.
		target, err := safeJoin(dir, src)
		if err != nil {
			return err
		}
		matches, err := filepath.Glob(target)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			// A mistyped folder or a glob that matches nothing would otherwise
			// import silently — surface it so the operator knows why a source is empty.
			log.Printf("outbox: sources entry %q matched no files under %s", src, dir)
			continue
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			fi, err := os.Stat(m)
			if err != nil {
				return err
			}
			if fi.IsDir() {
				if isGlob {
					// A glob matched a directory: don't recurse (single-level
					// semantics, matching Serves). A plain folder entry recurses.
					continue
				}
				if err := importTree(st, dir, m); err != nil {
					return err
				}
			} else if strings.HasSuffix(m, ".md") {
				if err := importFile(st, dir, m); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// importTree walks root recursively, importing every .md file keyed by its path
// relative to dir. root may be dir itself or a whitelisted sub-folder.
func importTree(st *store.Store, dir, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".outbox" || (strings.HasPrefix(d.Name(), ".") && p != root) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		return importFile(st, dir, p)
	})
}

// importFile imports a single .md file at absolute path p, keyed by its path
// relative to dir. Already-imported files are skipped.
func importFile(st *store.Store, dir, p string) error {
	rel, _ := filepath.Rel(dir, p)
	if _, ok, _ := st.GetDocumentByPath(rel); ok {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	_, _, err = st.CreateDocument(rel, string(b), "import")
	return err
}

// version is the CLI version. It is "dev" for local builds and overridden at
// release time via -ldflags "-X main.version=<v>".
var version = "dev"

// lookPath is a seam over exec.LookPath so tests can simulate an external
// binary (e.g. `claude`) being absent without shelling out to a real one.
var lookPath = exec.LookPath

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "outbox: "+err.Error())
		os.Exit(1)
	}
}

// resolveCmd maps raw args to a subcommand name and its remaining args. A bare
// invocation (no args) selects "serve", so the Docker ENTRYPOINT (which calls
// the binary with no args) keeps serving.
func resolveCmd(args []string) (name string, rest []string) {
	if len(args) == 0 {
		return "serve", nil
	}
	return args[0], args[1:]
}

// run is the testable core of main: it dispatches a subcommand. The
// version/help/unknown paths write to out and never open a listener, so routing
// can be exercised without a live server.
func run(args []string, out io.Writer) error {
	cmd, rest := resolveCmd(args)
	switch cmd {
	case "serve":
		return serve(rest, false)
	case "up":
		return serve(rest, true)
	case "init":
		return initProject(rest, out)
	case "upgrade":
		return upgrade(out)
	case "version", "--version", "-v":
		fmt.Fprintln(out, version)
		return nil
	case "help", "-h", "--help":
		usage(out)
		return nil
	default:
		usage(out)
		return fmt.Errorf("unknown command %q (run \"outbox help\")", cmd)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `outbox-md — local-first, agent-agnostic review for AI-generated Markdown specs.

Usage:
  outbox [command] [flags]

Commands:
  serve      Serve the review UI + MCP endpoint for a folder of .md files (default)
  up         Serve, then open the browser at the review UI
  init       Scaffold outbox.yaml + register the MCP with Claude in this folder
  upgrade    Update outbox to the latest release (self-update)
  version    Print the version
  help       Show this help

Flags (serve, up):
  -dir    folder of .md files to serve   (default ".",     overridden by OUTBOX_DIR)
  -addr   listen address                 (default ":8181", overridden by OUTBOX_ADDR)

Getting started:
  outbox init && outbox up
`)
}

// resolveFlags parses -dir/-addr for the server subcommands. Precedence is
// flag > env > default: the env value (if set) becomes the flag's default, so an
// explicit flag still wins over OUTBOX_DIR/OUTBOX_ADDR, which win over the
// built-in defaults. The default served dir is the current directory (".") for a
// local run; the Docker image sets OUTBOX_DIR=/data to keep serving /data.
func resolveFlags(name string, args []string, out io.Writer) (dir, addr string, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	d := fs.String("dir", getenv("OUTBOX_DIR", "."), "folder of .md files to serve")
	a := fs.String("addr", getenv("OUTBOX_ADDR", ":8181"), "listen address")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	return *d, *a, nil
}

// serve builds the server for dir and listens on addr. When open is true it
// binds the listener first, opens the browser, then serves (the `up` command),
// so the browser only opens once the port is actually accepting connections.
func serve(args []string, open bool) error {
	dir, addr, err := resolveFlags("serve", args, os.Stderr)
	if err != nil {
		return err
	}
	if open {
		// `up` only: a best-effort self-update BEFORE binding the port. It never
		// blocks or fails startup; on a successful update it re-execs, so the new
		// process (not this one) must be the one to bind the listener.
		maybeAutoUpdate(config.Load(dir), os.Stdout)
	}
	mux, err := buildServer(dir)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("outbox-md on %s serving %s (mcp at /mcp)", addr, dir)
	if open {
		openBrowser(browseURL(addr))
	}
	return http.Serve(ln, mux)
}

// buildServer wires the store, service, config, event fan-out and HTTP routes
// for dir, returning the ready handler. It is shared by `serve` and `up` so the
// bootstrap lives in exactly one place.
func buildServer(dir string) (http.Handler, error) {
	if err := ensureDataDir(dir); err != nil {
		return nil, err
	}
	dbDir := filepath.Join(dir, ".outbox")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, err
	}
	st, err := store.Open("file:" + filepath.Join(dbDir, "outbox.db"))
	if err != nil {
		return nil, err
	}
	svc := service.New(st, func(path, content string) error {
		target, err := safeJoin(dir, path)
		if err != nil {
			return err
		}
		return atomicWrite(target, content)
	})
	cfg := config.Load(dir)
	svc.SetConfig(cfg)
	// One governance event fans out to two sinks: the external HTTP runner
	// (webhook) and every open browser stream (SSE hub).
	hub := sse.NewHub()
	svc.SetWebhook(webhook.Fanout(webhook.New(cfg.Webhook), hub))
	if err := importMarkdown(st, dir, cfg.Sources); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/api/", api.NewAPI(svc, st, hub))

	// MCP over Streamable HTTP at /mcp — any agent connects here.
	mcpServer := mcp.NewServer(&mcp.Handlers{Svc: svc, St: st})
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return mcpServer }, nil)
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)

	sub, _ := fs.Sub(web.Dist, "dist")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux, nil
}

// browseURL turns a listen address (e.g. ":8181" or "localhost:9090") into a
// loopback URL for the browser and the MCP endpoint.
func browseURL(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8181"
	}
	return "http://localhost:" + port
}

// openBrowser best-effort opens url in the default browser. Any failure (no
// opener on PATH, unsupported OS, headless host) is ignored so it never blocks
// or fails serving.
func openBrowser(url string) {
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "open"
	case "linux":
		bin = "xdg-open"
	default:
		return
	}
	path, err := lookPath(bin)
	if err != nil {
		return
	}
	_ = exec.Command(path, url).Start()
}

// starterConfig is written by `outbox init` when no outbox.yaml exists. The
// sources block is commented out so the default (serve every .md under the dir)
// stays in effect until the user opts into a whitelist.
const starterConfig = `# outbox.yaml — configuration for outbox-md.
# By default outbox-md serves every .md file under this folder. To serve only
# part of a larger repo, uncomment "sources" and list folders and/or globs
# (relative to this file); paths stay project-relative.
#
# sources:
#   - docs/specs        # a folder → walked recursively
#   - rfcs              # another folder
#   - drafts/*.md       # a glob → matched files only (non-recursive)

# outbox up self-updates to the latest release by default. Set to false to opt
# out (you can still update on demand with "outbox upgrade"). Homebrew installs
# update via "brew upgrade"; Docker via image pull / Watchtower.
# auto_update: true
`

// initProject scaffolds onboarding in the target folder: it writes a starter
// outbox.yaml (never overwriting an existing one) and, when the `claude` CLI is
// present, registers this project's MCP endpoint. When claude is absent or the
// registration command fails, it prints the exact command instead of failing.
func initProject(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("dir", getenv("OUTBOX_DIR", "."), "folder to scaffold")
	addr := fs.String("addr", getenv("OUTBOX_ADDR", ":8181"), "listen address (for the MCP URL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := ensureDataDir(*dir); err != nil {
		return err
	}

	cfgPath := filepath.Join(*dir, "outbox.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Fprintf(out, "kept existing %s\n", cfgPath)
	} else if os.IsNotExist(err) {
		if err := os.WriteFile(cfgPath, []byte(starterConfig), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s\n", cfgPath)
	} else {
		return err
	}

	registerMCP(out, browseURL(*addr)+"/mcp")

	fmt.Fprintln(out, "\nNext: run `outbox up` to start the server and open the review UI.")
	return nil
}

// registerMCP registers outbox-md's MCP endpoint with the Claude CLI for this
// project. If `claude` is not on PATH, or the command fails (e.g. already
// registered), it prints the exact command for the user to run — registration
// is best-effort and never fatal to `init`.
func registerMCP(out io.Writer, mcpURL string) {
	argv := []string{"claude", "mcp", "add", "--transport", "http", "outbox-md", mcpURL}
	printable := strings.Join(argv, " ")
	path, err := lookPath("claude")
	if err != nil {
		fmt.Fprintf(out, "claude CLI not found — register the MCP yourself:\n  %s\n", printable)
		return
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(out, "could not register the MCP automatically — run:\n  %s\n", printable)
		return
	}
	fmt.Fprintf(out, "registered the outbox-md MCP with Claude:\n  %s\n", printable)
}

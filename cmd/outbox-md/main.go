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
	"github.com/rajanrx/outbox-md/internal/agentpreset"
	"github.com/rajanrx/outbox-md/internal/api"
	"github.com/rajanrx/outbox-md/internal/autoreply"
	"github.com/rajanrx/outbox-md/internal/config"
	"github.com/rajanrx/outbox-md/internal/mcp"
	"github.com/rajanrx/outbox-md/internal/mcpclients"
	"github.com/rajanrx/outbox-md/internal/registry"
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
func importMarkdown(st *store.Store, project, dir string, sources []string) error {
	if len(sources) == 0 {
		return importTree(st, project, dir, dir)
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
				if err := importTree(st, project, dir, m); err != nil {
					return err
				}
			} else if strings.HasSuffix(m, ".md") {
				if err := importFile(st, project, dir, m); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// importTree walks root recursively, importing every .md file keyed by its path
// relative to dir under the given project. root may be dir itself or a
// whitelisted sub-folder.
func importTree(st *store.Store, project, dir, root string) error {
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
		return importFile(st, project, dir, p)
	})
}

// importFile imports a single .md file at absolute path p, keyed by (project, its
// path relative to dir). Already-imported files (same project+path) are skipped.
func importFile(st *store.Store, project, dir, p string) error {
	rel, _ := filepath.Rel(dir, p)
	if _, ok, _ := st.GetDocumentByPath(project, rel); ok {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	_, _, err = st.CreateDocumentInProject(project, rel, string(b), "import")
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
	case "add":
		return addProject(rest, out)
	case "remove":
		return removeProject(rest, out)
	case "list", "projects":
		return listProjectsCmd(out)
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
  init       Scaffold outbox.yaml + auto-register the MCP with detected AI clients
  add        Register a project: outbox add <root> [docs] [--agent|--agent-cmd]
  remove     Unregister a project by name or root
  list       List registered projects (alias: projects)
  upgrade    Update outbox to the latest release (self-update)
  version    Print the version
  help       Show this help

Flags (serve, up):
  -dir    folder of .md files to serve   (default ".",     overridden by OUTBOX_DIR)
  -addr   listen address                 (default ":8181", overridden by OUTBOX_ADDR)

Flags (init):
  -dir     folder to scaffold             (default ".",     overridden by OUTBOX_DIR)
  -addr    listen address for the MCP URL (default ":8181", overridden by OUTBOX_ADDR)
  -client  register only this client (repeatable): claude-code, gemini, cursor,
           windsurf, claude-desktop, codex
  -all     register with every supported client, even if not detected

Getting started:
  outbox init && outbox up

Multiple projects:
  outbox add ~/work/app              # serve the whole repo (docs default ".")
  outbox add ~/work/app docs/specs   # serve only the docs/specs subpath of the repo
  outbox add ~/work/api --agent codex   # this project's auto-reply uses codex
  outbox up                          # serve ALL registered projects; switch in the UI

Add flags:
  <root>        project repo root (required positional, default ".")
  [docs]        spec subpath under root (optional positional, default ".")
  --agent       per-project agent preset: claude, codex, copilot
  --agent-cmd   per-project agent command with a {prompt} token (overrides --agent)

Auto-reply spawns the agent with cwd = the comment's project ROOT and that
project's agent command, so it runs inside that repo (its CLAUDE.md/.mcp.json).
`)
}

// resolveFlags parses -dir/-addr for the server subcommands. Precedence is
// flag > env > default: the env value (if set) becomes the flag's default, so an
// explicit flag still wins over OUTBOX_DIR/OUTBOX_ADDR, which win over the
// built-in defaults. The default served dir is the current directory (".") for a
// local run; the Docker image sets OUTBOX_DIR=/data to keep serving /data.
func resolveFlags(name string, args []string, out io.Writer) (dir, addr string, autoReply bool, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	d := fs.String("dir", getenv("OUTBOX_DIR", "."), "folder of .md files to serve")
	a := fs.String("addr", getenv("OUTBOX_ADDR", ":8181"), "listen address")
	// -auto-reply forces the in-process agent loop ON regardless of config. The
	// flag only turns it on (there is no force-off); when absent, the config
	// (auto_reply yaml / OUTBOX_AUTO_REPLY env / default false) decides.
	ar := fs.Bool("auto-reply", false, "spawn the agent CLI in-process on each human comment (opt-in)")
	if err := fs.Parse(args); err != nil {
		return "", "", false, err
	}
	return *d, *a, *ar, nil
}

// serve builds the server for dir and listens on addr. When open is true it
// binds the listener first, opens the browser, then serves (the `up` command),
// so the browser only opens once the port is actually accepting connections.
func serve(args []string, open bool) error {
	dir, addr, autoReply, err := resolveFlags("serve", args, os.Stderr)
	if err != nil {
		return err
	}
	if open {
		// `up` only: a best-effort self-update BEFORE binding the port. It never
		// blocks or fails startup; on a successful update it re-execs, so the new
		// process (not this one) must be the one to bind the listener.
		maybeAutoUpdate(config.Load(dir), os.Stdout)
	}
	projects := resolveProjects(dir)
	mux, err := buildServer(dir, projects, autoReply)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("outbox-md on %s serving %s (mcp at /mcp)", addr, describeProjects(projects, dir))
	if open {
		openBrowser(browseURL(addr))
	}
	return http.Serve(ln, mux)
}

// resolveProjects returns the projects to serve. When the global registry has at
// least one project, ALL of them are served. Otherwise it falls back to the
// single -dir with the empty project name — so a plain `outbox up` with no
// registry behaves exactly as it did before multi-project support.
func resolveProjects(dir string) []registry.Project {
	if projects, err := registry.Load(registryPath()); err == nil && len(projects) > 0 {
		return projects
	}
	return []registry.Project{{Name: "", Root: dir, Docs: "."}}
}

// describeProjects renders a short human summary of what is being served, for the
// startup log line.
func describeProjects(projects []registry.Project, dir string) string {
	if len(projects) == 1 && projects[0].Name == "" {
		return dir
	}
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Name
	}
	return fmt.Sprintf("%d projects [%s]", len(projects), strings.Join(names, ", "))
}

// buildServer wires the store, service, config, event fan-out and HTTP routes,
// returning the ready handler. It is shared by `serve` and `up` so the bootstrap
// lives in exactly one place.
//
// root is the folder that owns the database (root/.outbox/outbox.db) and the
// GLOBAL config (webhook + auto_update) — in single-folder mode it is the served
// dir, unchanged from before. projects is the set of folders to import & serve:
// a single {Name:"", Path:root} entry is the backward-compatible single-folder
// mode; two-or-more entries come from the registry. Each project keeps its OWN
// outbox.yaml sources (loaded per project); the per-project whitelist is enforced
// at import time (a hidden doc never enters the store).
func buildServer(root string, projects []registry.Project, autoReplyFlag bool) (http.Handler, error) {
	multi := !(len(projects) == 1 && projects[0].Name == "")

	// Database location. Single-folder mode keeps its database inside the served
	// folder (root/.outbox/outbox.db), unchanged from before. Multi-project mode
	// stores ONE shared database next to the registry, under the global config
	// home — a fresh location that (a) never collides with a pre-existing
	// single-folder database's legacy UNIQUE(path) constraint, and (b) keeps the
	// review state independent of which directory the server was launched from.
	var dbDir string
	if multi {
		dbDir = configHomeDir()
	} else {
		if err := ensureDataDir(root); err != nil {
			return nil, err
		}
		dbDir = filepath.Join(root, ".outbox")
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, err
	}
	st, err := store.Open("file:" + filepath.Join(dbDir, "outbox.db"))
	if err != nil {
		return nil, err
	}

	// Route a document's disk write to its project's spec dir (root/docs), keyed
	// by project name.
	dirByProject := make(map[string]string, len(projects))
	for _, p := range projects {
		dirByProject[p.Name] = p.SpecDir()
	}
	svc := service.New(st, func(project, path, content string) error {
		base, ok := dirByProject[project]
		if !ok {
			return fmt.Errorf("unknown project %q", project)
		}
		target, err := safeJoin(base, path)
		if err != nil {
			return err
		}
		return atomicWrite(target, content)
	})

	cfg := config.Load(root)
	if multi {
		// Only webhook + auto_update are global (loaded from root). Sources are
		// per-project (see the per-project map below) — so the root's sources must
		// NOT leak into the global cfg surfaced at /api/config. The runtime guards
		// read the per-project map, not cfg.Sources.
		cfg.Sources = nil
	}
	svc.SetConfig(cfg)
	svc.SetProjects(projects)
	// One governance event fans out to the external HTTP runner (webhook), every
	// open browser stream (SSE hub), and — when enabled — the in-process
	// auto-reply engine that spawns the agent CLI on each human comment.
	hub := sse.NewHub()
	sinks := []webhook.Notifier{webhook.New(cfg.Webhook), hub}
	if engine := autoReplyNotifier(root, projects, cfg, autoReplyFlag); engine != nil {
		sinks = append(sinks, engine)
		log.Printf("auto-reply: on (default agent: %s; per-project roots + agents from the registry)", cfg.AgentCmd)
	}
	svc.SetWebhook(webhook.Fanout(sinks...))
	// Per-project runtime sources: each served project → its OWN loaded config, so
	// the read guards enforce that project's Sources whitelist against its own
	// docs. This makes the #35 runtime guard project-aware — narrowing a project's
	// outbox.yaml sources hides its previously-imported docs on every surface
	// (UI/HTTP/MCP), not just at import. Single-folder mode is one entry keyed ""
	// carrying the real cfg, so single-dir behaviour is bit-for-bit unchanged.
	sources := make(config.ProjectSources, len(projects))
	// Import every project under its own name, honouring that project's own
	// sources whitelist. Single-folder mode imports under the empty project.
	for _, p := range projects {
		specDir := p.SpecDir()
		if err := ensureDataDir(specDir); err != nil {
			return nil, err
		}
		pcfg := config.Load(specDir)
		sources[p.Name] = pcfg
		if err := importMarkdown(st, p.Name, specDir, pcfg.Sources); err != nil {
			return nil, err
		}
	}
	svc.SetProjectSources(sources)

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

// autoReplyNotifier decides whether the in-process auto-reply engine should be
// wired into the event fan-out, and builds it when so. It is a pure function
// (no listener, no side effects) so the wiring decision is unit-testable without
// a live server: it returns nil when auto-reply is off, and a live engine when
// on. Precedence is flag > config: the -auto-reply flag forces it on regardless
// of config (it only turns ON), otherwise cfg.AutoReply (auto_reply yaml /
// OUTBOX_AUTO_REPLY env / default false) decides. root is the served dir used as
// the default (fallback) working directory; each project overrides it with its
// own root + agent command (see the Targets map).
func autoReplyNotifier(root string, projects []registry.Project, cfg config.Config, flagOn bool) webhook.Notifier {
	if !(flagOn || cfg.AutoReply) {
		return nil
	}
	// Build a project → {root, agentCmd} map from the registry. A triggering
	// comment resolves to its project's ROOT (so the agent runs inside that repo,
	// seeing its CLAUDE.md/.mcp.json/codebase) and that project's agent command
	// (empty ⇒ the global default below). Single-folder mode is one entry keyed ""
	// whose root is the served dir — preserving the original single-cwd behaviour.
	targets := make(map[string]autoreply.Target, len(projects))
	for _, p := range projects {
		targets[p.Name] = autoreply.Target{Root: p.Root, AgentCmd: p.Agent}
	}
	return autoreply.New(autoreply.Config{
		Enabled:  true,
		Dir:      root,
		AgentCmd: cfg.AgentCmd,
		Targets:  targets,
	})
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

# Hands-off auto-reply (opt-in, default OFF). When on, a human comment spawns the
# agent CLI in-process (no separate runner) to claim + propose/reply. It reacts
# only to YOUR comments, never its own. Turn on here, with OUTBOX_AUTO_REPLY=true,
# or per-run with "outbox up --auto-reply". agent_cmd is the spawned command;
# {prompt} is replaced with the instruction. The default reuses your Claude
# subscription via the CLI, so there is no API cost.
# auto_reply: false
# agent_cmd: claude -p {prompt} --allowedTools mcp__outbox-md__*
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
	all := fs.Bool("all", false, "register with every supported AI client, even if not detected")
	var clientFlags stringSliceFlag
	fs.Var(&clientFlags, "client", "register only this client (repeatable): "+strings.Join(mcpclients.Slugs(), ", "))
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

	if err := registerMCPClients(out, browseURL(*addr)+"/mcp", *all, clientFlags); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nNext: run `outbox up` to start the server and open the review UI.")
	return nil
}

// stringSliceFlag collects a repeatable string flag (e.g. -client a -client b).
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// registerMCPClients detects installed AI clients and registers outbox-md's MCP
// endpoint (mcpURL) with each. Default is auto-detect; -all forces every client;
// -client targets specific ones. It prints a per-client summary and never fails
// init because a single client could not be wired (only a bad -client name is
// fatal, surfaced as a usage error).
func registerMCPClients(out io.Writer, mcpURL string, all bool, only []string) error {
	home, _ := os.UserHomeDir()
	env := mcpclients.Env{
		HomeDir:       home,
		GOOS:          runtime.GOOS,
		CommandExists: func(name string) bool { _, err := lookPath(name); return err == nil },
		DirExists:     func(path string) bool { fi, err := os.Stat(path); return err == nil && fi.IsDir() },
		ReadFile:      os.ReadFile,
		WriteFile:     os.WriteFile,
		MkdirAll:      os.MkdirAll,
		RunCommand: func(name string, args []string) error {
			cmd := exec.Command(name, args...)
			cmd.Stdout, cmd.Stderr = out, out
			return cmd.Run()
		},
	}

	results, err := mcpclients.Register(env, mcpURL, mcpclients.Options{All: all, Only: only})
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "\nAI clients:")
	for _, r := range results {
		switch r.Action {
		case mcpclients.ActionWired:
			fmt.Fprintf(out, "  ✓ %s — registered (%s)\n", r.Name, r.Detail)
		case mcpclients.ActionNoted:
			fmt.Fprintf(out, "  ✓ %s — %s\n", r.Name, r.Note)
		case mcpclients.ActionSkipped:
			fmt.Fprintf(out, "  · %s — not detected (install it, then re-run `outbox init`)\n", r.Name)
		case mcpclients.ActionFailed:
			fmt.Fprintf(out, "  ✗ %s — could not register: %v\n", r.Name, r.Err)
		}
	}
	return nil
}

// configHomeDir resolves outbox-md's global config directory. It honours
// XDG_CONFIG_HOME first (so the location is overridable and testable across
// platforms, including macOS where os.UserConfigDir ignores XDG), then falls
// back to the OS user-config directory. The directory is global: it is shared by
// every `outbox` invocation regardless of the current directory.
func configHomeDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		if d, err := os.UserConfigDir(); err == nil {
			base = d
		} else {
			base = "." // last resort: keep the CLI usable even without a home dir
		}
	}
	return filepath.Join(base, "outbox")
}

// registryPath is the global projects-registry file, under the config home.
func registryPath() string { return filepath.Join(configHomeDir(), "projects.json") }

// addProject registers a project: `outbox add <root> [docs] [--agent <preset> |
// --agent-cmd <cmd>]`. root (required, default ".") is the project repo root;
// docs (optional positional, default ".") is the spec subpath under root. The
// per-project agent command is resolved from --agent-cmd (an explicit command
// with a {prompt} token, which wins) or --agent (a preset name); when neither is
// given the project uses the global default at auto-reply time. Registration is
// idempotent by (root, docs) and names are kept unique (see registry.Add).
func addProject(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(out)
	agentPreset := fs.String("agent", "", "per-project agent preset: "+strings.Join(agentpreset.Names(), ", "))
	agentCmd := fs.String("agent-cmd", "", "per-project agent command with a {prompt} token (overrides --agent)")
	// Go's flag package stops at the first positional, so `add <root> <docs>
	// --agent x` would ignore the trailing flag. Parse in a loop, peeling off one
	// positional at a time and re-parsing the remainder, so flags may appear
	// anywhere (before, between, or after the positionals).
	var positionals []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		positionals = append(positionals, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	root := "."
	if len(positionals) > 0 {
		root = positionals[0]
	}
	docs := "."
	if len(positionals) > 1 {
		docs = positionals[1]
	}
	// Resolve the agent command: --agent-cmd (explicit) wins over --agent (preset);
	// empty when neither is set (the project inherits the global default).
	agent := strings.TrimSpace(*agentCmd)
	if agent == "" && strings.TrimSpace(*agentPreset) != "" {
		resolved, err := agentpreset.ResolveOrError(strings.TrimSpace(*agentPreset))
		if err != nil {
			return err
		}
		agent = resolved
	}
	p, err := registry.Add(registryPath(), root, docs, agent)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "registered project %q → %s (docs: %s)", p.Name, p.Root, p.Docs)
	if p.Agent != "" {
		fmt.Fprintf(out, " [agent: %s]", p.Agent)
	}
	fmt.Fprintln(out)
	return nil
}

// removeProject unregisters a project by name or path.
func removeProject(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(out)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: outbox remove <name|path>")
	}
	ref := fs.Arg(0)
	removed, err := registry.Remove(registryPath(), ref)
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintf(out, "no project matching %q\n", ref)
		return nil
	}
	fmt.Fprintf(out, "removed project %q\n", ref)
	return nil
}

// listProjectsCmd prints the registered projects.
func listProjectsCmd(out io.Writer) error {
	projects, err := registry.List(registryPath())
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintln(out, "no projects registered — run `outbox add [path]` to register one")
		return nil
	}
	for _, p := range projects {
		loc := p.Root
		if p.Docs != "" && p.Docs != "." {
			loc = p.Root + "/" + p.Docs
		}
		line := fmt.Sprintf("%s\t%s", p.Name, loc)
		if p.Agent != "" {
			line += "\t[" + p.Agent + "]"
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

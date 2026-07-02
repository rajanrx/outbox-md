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
	"github.com/rajanrx/outbox-md/internal/domain"
	"github.com/rajanrx/outbox-md/internal/mcp"
	"github.com/rajanrx/outbox-md/internal/mcpclients"
	"github.com/rajanrx/outbox-md/internal/registry"
	"github.com/rajanrx/outbox-md/internal/service"
	"github.com/rajanrx/outbox-md/internal/sse"
	"github.com/rajanrx/outbox-md/internal/store"
	"github.com/rajanrx/outbox-md/internal/watcher"
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
	// Defense in depth: if target resolves (an existing dir/file, not a glob),
	// re-check containment on the symlink-resolved paths — a symlinked component
	// can escape dir while passing the lexical check. Skip when it can't resolve
	// (globs / not-yet-created paths); the lexical guard above still applies there.
	if rt, e1 := filepath.EvalSymlinks(target); e1 == nil {
		if rd, e2 := filepath.EvalSymlinks(dir); e2 == nil {
			if r, e3 := filepath.Rel(rd, rt); e3 != nil || r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
				return "", fmt.Errorf("refusing to write outside managed dir: %q", path)
			}
		}
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

// importMarkdown ingests the .md files of one docs subtree. It walks dir (a
// project's root/docsN spec dir) recursively and keys each file by its path
// relative to keyBase — keyBase is the project ROOT, so a doc under a docs
// subtree is keyed root-relative and the same filename under two different
// subtrees never collides. In single-subtree mode keyBase == dir, so keys stay
// project-relative exactly as before.
//
// A file is imported iff its ROOT-RELATIVE key passes config.SourcesMatch — the
// SAME predicate the served set gates on. Because the walk already stays within
// a docs subtree, "under docsN" is guaranteed by construction; the sources
// filter is evaluated against the root-relative key (never joined onto dir), so
// the imported set is exactly the served set (no import/serve drift). An empty
// sources list applies no filter (import every .md in the subtree).
func importMarkdown(st *store.Store, project, keyBase, dir string, sources []string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".outbox" || (strings.HasPrefix(d.Name(), ".") && p != dir) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(keyBase, p)
		if err != nil {
			return err
		}
		if !config.SourcesMatch(sources, filepath.ToSlash(rel)) {
			return nil
		}
		return importFile(st, project, keyBase, p)
	})
}

// importFile imports a single .md file at absolute path p, keyed by (project, its
// path relative to keyBase). Already-imported files (same project+path) are
// skipped.
func importFile(st *store.Store, project, keyBase, p string) error {
	rel, _ := filepath.Rel(keyBase, p)
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

// run is the testable core of main: it dispatches a subcommand. Bare `outbox`
// (no args) prints HELP rather than starting a server, so the binary is
// discoverable; the Docker image pins an explicit "serve" in its CMD. The
// version/help/unknown/usage-error paths write to out and never open a listener,
// so routing can be exercised without a live server.
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		usage(out)
		return nil
	}
	cmd, rest := args[0], args[1:]
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
		return removeProject(rest, out, os.Stdin)
	case "list", "projects":
		return listProjectsCmd(out)
	case "retry":
		return retryCmd(rest, out)
	case "paths":
		return pathsCmd(out)
	case "settings":
		return settingsCmd(rest, out, os.Stdin)
	case "upgrade":
		return upgrade(out)
	case "version", "--version", "-v":
		fmt.Fprintln(out, version)
		return nil
	case "help", "-h", "--help":
		return helpCommand(rest, out)
	default:
		usage(out)
		return fmt.Errorf("unknown command %q (run \"outbox help\")", cmd)
	}
}

// resolveFlags parses -dir/-addr for the server subcommands. Precedence is
// flag > env > default: the env value (if set) becomes the flag's default, so an
// explicit flag still wins over OUTBOX_DIR/OUTBOX_ADDR, which win over the
// built-in defaults. The default served dir is the current directory (".") for a
// local run; the Docker image sets OUTBOX_DIR=/data to keep serving /data.
func resolveFlags(name string, args []string, out io.Writer) (dir, addr string, autoReply, logs bool, err error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { usageFor(out, name) }
	d := fs.String("dir", getenv("OUTBOX_DIR", "."), "folder of .md files to serve")
	a := fs.String("addr", getenv("OUTBOX_ADDR", ":8181"), "listen address")
	// -auto-reply forces the in-process agent loop ON regardless of config. The
	// flag only turns it on (there is no force-off); when absent, the config
	// (auto_reply yaml / OUTBOX_AUTO_REPLY env / default false) decides.
	ar := fs.Bool("auto-reply", false, "spawn the agent CLI in-process on each human comment (opt-in)")
	// -logs gates whether the auto-reply agent's OUTPUT (its thinking stream) is
	// printed. It defaults ON; OUTBOX_LOGS overrides the default, and the explicit
	// flag wins over the env. Off ⇒ only lifecycle lines (invoking/complete/failed).
	lg := fs.Bool("logs", envBool("OUTBOX_LOGS", true), "print the auto-reply agent's output/thinking stream")
	if err := fs.Parse(args); err != nil {
		return "", "", false, false, err
	}
	return *d, *a, *ar, *lg, nil
}

// envBool reads a loose boolean env var (true/1/yes/on, false/0/no/off),
// returning def when the var is unset or unrecognised.
func envBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return def
}

// serve builds the server for dir and listens on addr. When open is true it
// binds the listener first, opens the browser, then serves (the `up` command),
// so the browser only opens once the port is actually accepting connections.
func serve(args []string, open bool) error {
	name := "serve"
	if open {
		name = "up"
	}
	dir, addr, autoReply, logs, err := resolveFlags(name, args, os.Stderr)
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
	mux, stop, err := buildServer(dir, projects, autoReply, logs)
	if err != nil {
		return err
	}
	// http.Serve blocks until the listener errors; on return, stop the watcher so
	// its goroutine and fsnotify handle are released (no leak in tests / re-exec).
	defer stop()
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
	return []registry.Project{{Name: "", Root: dir, Docs: []string{"."}}}
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
func buildServer(root string, projects []registry.Project, autoReplyFlag, logs bool) (http.Handler, func(), error) {
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
			return nil, nil, err
		}
		dbDir = filepath.Join(root, ".outbox")
	}
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, nil, err
	}
	st, err := store.Open("file:" + filepath.Join(dbDir, "outbox.db"))
	if err != nil {
		return nil, nil, err
	}

	// Route a document's disk write to its project ROOT, keyed by project name.
	// Docs are keyed relative to Root (so "docs1/a.md" vs "docs2/a.md" stay
	// distinct), so the write target is safeJoin(root, key).
	dirByProject := make(map[string]string, len(projects))
	for _, p := range projects {
		dirByProject[p.Name] = p.Root
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
	svc.SetVersion(version)
	// Registry-backed (multi-project) mode exposes the projects CRUD API, which
	// persists through the shared projects.json. Single-folder mode has no registry
	// to write to, so the path stays empty and the CRUD endpoints reject writes.
	if multi {
		svc.SetRegistryPath(registryPath())
	}
	// One governance event fans out to the external HTTP runner (webhook), every
	// open browser stream (SSE hub), and — when enabled — the in-process
	// auto-reply engine that spawns the agent CLI on each human comment.
	hub := sse.NewHub()
	sinks := []webhook.Notifier{webhook.New(cfg.Webhook), hub}
	var engine *autoreply.Engine
	if e := autoReplyNotifier(root, projects, cfg, autoReplyFlag, logs, svc); e != nil {
		engine = e
		sinks = append(sinks, e)
		log.Printf("auto-reply: on (default agent: %s; per-project roots + agents from the registry; "+
			"retries=%d timeout=%s concurrency=%d logs=%t)", cfg.AgentCmd, cfg.Agent.ResolveRetries(),
			cfg.Agent.ResolveTimeout(), cfg.Agent.ResolveConcurrency(), logs)
	}
	svc.SetWebhook(webhook.Fanout(sinks...))
	// Per-project runtime sources: each served project → its OWN loaded config, so
	// the read guards enforce that project's Sources whitelist against its own
	// docs. This makes the #35 runtime guard project-aware — narrowing a project's
	// outbox.yaml sources hides its previously-imported docs on every surface
	// (UI/HTTP/MCP), not just at import. Single-folder mode is one entry keyed ""
	// carrying the real cfg, so single-dir behaviour is bit-for-bit unchanged.
	sources := make(config.ProjectSources, len(projects))
	// Import every project under its own name. A project serves the UNION of its
	// docs subtrees; each subtree is walked but keyed relative to the project ROOT,
	// so the same filename under two subpaths (docs1/a.md vs docs2/a.md) never
	// collides. The per-project config (incl. its sources whitelist) loads from
	// root/outbox.yaml and is also installed as the runtime read guard. sources is
	// honoured within a single-root docs="." project (the classic "serve part of a
	// repo" case, e.g. the guard test); with an explicit multi-entry docs list the
	// docs entries themselves define the served set. Single-folder mode is one
	// entry keyed "" whose root is the served dir.
	for _, p := range projects {
		pcfg := config.Load(p.Root)
		// The runtime coverage gates on the SAME root-relative predicate import
		// uses: the docs union (from the registry) narrowed by the project's
		// root-relative sources (from its outbox.yaml). Threading docs here is what
		// hides docs OUTSIDE the current docs list (stale rows, previously-imported
		// dirs) on every read surface — not just the sources filter.
		sources[p.Name] = config.Coverage{Docs: p.DocRoots(), Sources: pcfg.Sources}
		for _, specDir := range p.SpecDirs() {
			if err := ensureDataDir(specDir); err != nil {
				return nil, nil, err
			}
			if err := importMarkdown(st, p.Name, p.Root, specDir, pcfg.Sources); err != nil {
				return nil, nil, err
			}
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

	// Live reload: watch each project's spec dirs so a .md created/edited/deleted
	// while the server runs shows up in the UI without a restart. The watcher fires
	// docs.changed on the SSE hub DIRECTLY (never the webhook fan-out), so it can
	// never trigger the auto-reply engine. It is best-effort: if fsnotify is
	// unavailable the server still serves, just without live reload.
	stop := func() {}
	wp := make([]watcher.Project, 0, len(projects))
	for _, p := range projects {
		wp = append(wp, watcher.Project{Name: p.Name, Root: p.Root, SpecDirs: p.SpecDirs()})
	}
	if w, err := watcher.New(wp, svc, hub, 0); err != nil {
		log.Printf("watcher: disabled (%v)", err)
	} else {
		w.Start()
		stop = func() { _ = w.Close() }
	}
	// Compose engine shutdown into stop so a server teardown cancels in-flight
	// retry backoff, and kick a startup sweep so any stranded backlog (open or
	// stale-claimed) is processed on boot without waiting for a fresh human comment.
	if engine != nil {
		prev := stop
		stop = func() {
			prev()
			engine.Close()
		}
		go engine.Sweep()
	}
	return mux, stop, nil
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
func autoReplyNotifier(root string, projects []registry.Project, cfg config.Config, flagOn, logs bool, svc *service.Service) *autoreply.Engine {
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
		// Members + Chair drive council mode (>= 2 members + a chair); AgentCmd is the
		// single-agent fallback (the lone member, or the global default). A council
		// Target only actually runs the council when the seams below are wired too.
		targets[p.Name] = autoreply.Target{
			Root:     p.Root,
			AgentCmd: p.AgentCmd(),
			Members:  p.MemberCmds(),
			Chair:    p.ChairCmd(),
		}
	}
	return autoreply.New(autoreply.Config{
		Enabled:  true,
		Dir:      root,
		AgentCmd: cfg.AgentCmd,
		Targets:  targets,
		// Resilience: retry a failed run (default 5) with backoff, and cap each run
		// at the (bumped, configurable) timeout. Config supplies the defaults, so an
		// explicit agent.retries: 0 is honoured as no-retry.
		Retries: cfg.Agent.ResolveRetries(),
		Timeout: cfg.Agent.ResolveTimeout(),
		// Fan-out: run up to N agent processes at once per project (default 4).
		// Claim atomicity (store CAS) keeps two agents off the same comment.
		Concurrency: cfg.Agent.ResolveConcurrency(),
		// Gate the agent's output/thinking stream on the -logs flag (default on).
		Logs: logs,
		// Drain a partly-cleared burst: after each run the engine re-checks how
		// much work remains for the project and runs again while it keeps making
		// progress, so a burst one run only partly handled is not stranded. Nil svc
		// is impossible here (main always builds one), but the engine also tolerates
		// a nil counter by disabling the drain.
		PendingCount: func(project string) (int, error) { return svc.PendingCommentCount(project) },
		// Council orchestration seams. All three wired ⇒ a project with >= 2 members
		// and a chair runs the council (engine claims each open comment, fans the
		// lensed members out, heartbeats for the whole run, then the chair synthesises).
		// Single-agent projects ignore these entirely.
		//
		// Claim claims ONE comment via the store CAS under a fixed "council" agent id,
		// returning the shared token and whether this run won it (won ⇒ len == 1).
		Claim: func(commentID string) (string, bool, error) {
			token, won, err := svc.Claim([]string{commentID}, councilAgentID)
			if err != nil {
				return "", false, err
			}
			return token, len(won) == 1, nil
		},
		// OpenComments lists the project's open (+ stale-claimed) comments as refs,
		// carrying the doc id + flagged excerpt + thread so members have full review
		// context (a claimed comment is hidden from list_open_comments). The service
		// applies the sources whitelist, so hidden docs never surface here.
		OpenComments: func(project string) ([]autoreply.CommentRef, error) {
			comments, err := svc.OpenCouncilComments(project)
			if err != nil {
				return nil, err
			}
			refs := make([]autoreply.CommentRef, 0, len(comments))
			for _, c := range comments {
				refs = append(refs, autoreply.CommentRef{
					ID:      c.CommentID,
					DocID:   c.DocID,
					DocPath: c.DocPath,
					Excerpt: c.Excerpt,
					Thread:  formatCouncilThread(c.Thread),
				})
			}
			return refs, nil
		},
		// Heartbeat re-marks the claimed comment as processing (default TTL) for the
		// whole council run so it never goes stale mid-run (→ double-council).
		Heartbeat: func(commentID, token string) error {
			_, err := svc.MarkProcessing(commentID, token, 0)
			return err
		},
	})
}

// councilAgentID is the fixed agent identity the engine claims council comments
// under (one council-run per comment; distinct member/chair identities travel in
// the prompts, not the claim).
const councilAgentID = "council"

// formatCouncilThread renders a comment's thread as "author: body" lines for the
// council member prompt (members can't fetch the thread from a claimed comment).
func formatCouncilThread(msgs []domain.ThreadMessage) string {
	if len(msgs) == 0 {
		return "(no messages yet)"
	}
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s: %s\n", m.AuthorIdentity, m.Body)
	}
	return strings.TrimSpace(b.String())
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
	fs.Usage = func() { usageFor(out, "init") }
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

// dbPath resolves the review database file for the current mode, mirroring
// buildServer's DSN exactly: multi-project mode shares ONE database next to the
// registry (config home); single-folder mode keeps it inside the served dir
// (<OUTBOX_DIR>/.outbox/outbox.db). Keeping this in lock-step with buildServer is
// essential — a drifting path would open a fresh empty DB and make `outbox retry`
// silently re-queue nothing.
func dbPath(multi bool, dir string) string {
	if multi {
		return filepath.Join(configHomeDir(), "outbox.db")
	}
	return filepath.Join(dir, ".outbox", "outbox.db")
}

// retryCmd re-queues stranded (claimed-but-unfinished) comments back to open so a
// running server picks them up on its next trigger / startup sweep, or a stopped
// one processes them on next boot. It operates on the store directly, so it works
// whether or not the server is running. Multi-project mode: no arg re-queues every
// registered project (one count per project); <name> targets one (an unknown name
// errors with the retry help). Single-folder mode targets project "". A missing
// review database is reported (nothing to do), never created.
func retryCmd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("retry", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { usageFor(out, "retry") }
	// -dir resolves the single-folder DB the same way serve/up does (flag >
	// OUTBOX_DIR > "."), so `outbox retry -dir docs` finds docs/.outbox/outbox.db
	// rather than looking under the cwd.
	dir := fs.String("dir", getenv("OUTBOX_DIR", "."), "single-folder served dir (to locate its .outbox DB)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	projects, err := registry.Load(registryPath())
	if err != nil {
		return err
	}
	multi := len(projects) > 0

	// Resolve the target set FIRST (before touching the DB), so an invalid name is
	// rejected with the retry help regardless of whether a database exists yet.
	var targets []registry.Project
	if fs.NArg() > 0 {
		if !multi {
			usageFor(out, "retry")
			return fmt.Errorf("no projects registered — run \"outbox retry\" with no name in single-folder mode")
		}
		name := fs.Arg(0)
		for _, p := range projects {
			if p.Name == name {
				targets = []registry.Project{p}
				break
			}
		}
		if len(targets) == 0 {
			usageFor(out, "retry")
			return fmt.Errorf("no project matching %q (run \"outbox list\")", name)
		}
	} else if multi {
		targets = projects
	} else {
		// Single-folder mode: the one (empty-named) project.
		targets = []registry.Project{{Name: ""}}
	}

	file := dbPath(multi, *dir)
	if _, err := os.Stat(file); os.IsNotExist(err) {
		fmt.Fprintf(out, "no review database at %s — nothing to re-queue\n", file)
		return nil
	} else if err != nil {
		return err
	}
	st, err := store.Open("file:" + file)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	for _, p := range targets {
		n, err := st.RequeueClaimedCommentsForProject(p.Name)
		if err != nil {
			return err
		}
		if multi {
			fmt.Fprintf(out, "re-queued %d in %s\n", n, p.Name)
		} else {
			fmt.Fprintf(out, "re-queued %d\n", n)
		}
	}
	return nil
}

// addProject registers a project: `outbox add <root> <docs...> [--agent <preset>]…
// [--agent-cmd <cmd>]… [--chair <preset> | --chair-cmd <cmd>]`. root (required) is
// the project repo root and must be an existing directory; docs is ONE OR MORE
// spec subpaths under root ("." is a valid, explicit entry meaning the whole repo)
// — zero docs is an error. --agent and --agent-cmd are REPEATABLE: each appends a
// council member (a preset resolves to its command; --agent-cmd is a raw command).
// --chair / --chair-cmd set the verdict-synthesising chair. Council rule: with two
// or more members a chair is REQUIRED (add prints the help and errors otherwise).
// Zero members ⇒ the project inherits the global default at auto-reply time; one
// member is single-agent mode. Registration is idempotent by (root, docs-set) and
// names are kept unique (see registry.Add). A missing/invalid argument prints the
// add usage + examples and returns an error.
func addProject(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(out)
	// On a bad flag, print the add usage + examples (not just Go's terse error).
	fs.Usage = func() { usageFor(out, "add") }
	var agentPresets stringSliceFlag
	fs.Var(&agentPresets, "agent", "council member preset, optionally preset:model (repeatable): "+strings.Join(agentpreset.Names(), ", "))
	var agentCmds stringSliceFlag
	fs.Var(&agentCmds, "agent-cmd", "council member command with a {prompt} token (repeatable; model embedded by you)")
	chairPreset := fs.String("chair", "", "council chair preset, optionally preset:model (required with >=2 members): "+strings.Join(agentpreset.Names(), ", "))
	chairCmd := fs.String("chair-cmd", "", "council chair command with a {prompt} token (overrides --chair)")
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
	// root + at least one docs are required. Zero positionals (no root) or a single
	// positional (root but no docs) both fail with the add help + examples.
	if len(positionals) < 2 {
		usageFor(out, "add")
		if len(positionals) == 0 {
			return fmt.Errorf("add requires a project root and at least one docs path")
		}
		return fmt.Errorf("add requires at least one docs path (use \".\" for the whole repo)")
	}
	root := positionals[0]
	docs := positionals[1:]
	// Members: each --agent names a preset (with an optional :model suffix, e.g.
	// "claude:opus"); each --agent-cmd is a raw command (its model is the user's
	// business — the :model syntax does NOT apply). Presets are listed first, then
	// raw commands, each preserving its own within-flag order. The member stores the
	// preset NAME + model (not a resolved command); resolution to the spawn command
	// (with the model flag injected) happens in registry.Member.Command. An empty
	// member list ⇒ inherit the global default.
	var members []registry.Member
	for _, spec := range agentPresets {
		name, model := splitAgentSpec(spec)
		if _, err := agentpreset.ResolveOrError(name); err != nil {
			return err
		}
		members = append(members, registry.Member{Agent: name, Model: model})
	}
	for _, c := range agentCmds {
		if s := strings.TrimSpace(c); s != "" {
			members = append(members, registry.Member{Agent: s})
		}
	}
	// Chair: --chair-cmd (explicit raw command) wins over --chair (preset[:model]).
	var chair registry.Member
	if s := strings.TrimSpace(*chairCmd); s != "" {
		chair = registry.Member{Agent: s}
	} else if s := strings.TrimSpace(*chairPreset); s != "" {
		name, model := splitAgentSpec(s)
		if _, err := agentpreset.ResolveOrError(name); err != nil {
			return err
		}
		chair = registry.Member{Agent: name, Model: model}
	}
	// Surface the council rule with the add help before hitting the registry.
	if len(members) >= 2 && strings.TrimSpace(chair.Agent) == "" {
		usageFor(out, "add")
		return fmt.Errorf("add: council mode (%d members) requires --chair <preset> or --chair-cmd \"<cmd>\"", len(members))
	}
	p, err := registry.Add(registryPath(), root, docs, members, chair)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "registered project %q → %s (docs: %s)", p.Name, p.Root, strings.Join(p.Docs, ", "))
	if p.IsCouncil() {
		fmt.Fprintf(out, " [%s → chair: %s]", strings.Join(p.MemberCmds(), ", "), p.ChairCmd())
	} else if cmd := p.AgentCmd(); cmd != "" {
		fmt.Fprintf(out, " [%s]", cmd)
	}
	fmt.Fprintln(out)
	return nil
}

// splitAgentSpec splits a `--agent`/`--chair` preset spec into its preset name and
// optional model: "claude:opus" → ("claude", "opus"); "claude" → ("claude", "").
// Only the FIRST colon splits (a model may itself contain a colon), and both sides
// are trimmed. This applies to preset flags only — a raw --agent-cmd is verbatim.
func splitAgentSpec(spec string) (name, model string) {
	spec = strings.TrimSpace(spec)
	if i := strings.Index(spec, ":"); i >= 0 {
		return strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i+1:])
	}
	return spec, ""
}

// removeProject unregisters registered projects (or individual docs subpaths).
// With a name/root argument it removes the WHOLE matching project (the
// non-interactive, back-compatible shortcut); an unknown ref is an error. With no
// argument it runs the interactive multiselect (rows = every project × each of its
// docs), removing the ticked docs entries and dropping any project whose last
// docs entry goes. When stdin is not a terminal and no argument is given it prints
// a hint and returns an error rather than hanging — mirroring the settings guard.
func removeProject(args []string, out io.Writer, stdin io.Reader) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { usageFor(out, "remove") }
	if err := fs.Parse(args); err != nil {
		return err
	}

	// No argument: interactive multiselect, guarded against a non-TTY stdin.
	if fs.NArg() == 0 {
		if !isTerminal(stdin) {
			return fmt.Errorf("remove needs a project name (non-interactive), or run in a terminal for multiselect")
		}
		return removeInteractive(out, stdin)
	}

	// `remove <name|root>`: remove the whole matching project (back-compat).
	ref := fs.Arg(0)
	removed, err := registry.Remove(registryPath(), ref)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no project matching %q", ref)
	}
	fmt.Fprintf(out, "removed project %q\n", ref)
	return nil
}

// listProjectsCmd prints the registered projects: name, its root + docs
// location(s), and [agent] when set.
func listProjectsCmd(out io.Writer) error {
	projects, err := registry.List(registryPath())
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintln(out, "no projects registered — run `outbox add <root> <docs...>` to register one")
		return nil
	}
	for _, p := range projects {
		line := fmt.Sprintf("%s\t%s", p.Name, projectLocations(p))
		if p.IsCouncil() {
			line += "\t[" + strings.Join(p.MemberCmds(), ", ") + " → chair: " + p.ChairCmd() + "]"
		} else if cmd := p.AgentCmd(); cmd != "" {
			line += "\t[" + cmd + "]"
		}
		fmt.Fprintln(out, line)
	}
	return nil
}

// projectLocations renders a project's served location(s) for `list`: the bare
// root when it serves the whole repo (docs ["."]/empty), else the root joined
// with each docs subpath, comma-separated.
func projectLocations(p registry.Project) string {
	docs := p.Docs
	if len(docs) == 0 {
		docs = []string{"."}
	}
	locs := make([]string, 0, len(docs))
	for _, d := range docs {
		if d == "" || d == "." {
			locs = append(locs, p.Root)
		} else {
			locs = append(locs, p.Root+"/"+d)
		}
	}
	return strings.Join(locs, ", ")
}

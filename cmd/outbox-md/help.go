package main

import (
	"fmt"
	"io"
	"sort"
)

// commandUsage maps a subcommand to its own usage + EXAMPLES help text. Every
// command prints THIS (not a terse Go flag error) when a required argument is
// missing or invalid, so a user always sees how to invoke the command and a
// worked example. Keep each entry self-contained: a one-line synopsis, the
// flags/positionals, then an EXAMPLES block.
var commandUsage = map[string]string{
	"serve": `outbox serve — serve the review UI + MCP endpoint for a folder of .md files.

Usage:
  outbox serve [-dir <folder>] [-addr <host:port>] [-auto-reply] [-logs=<bool>]

Flags:
  -dir          folder of .md files to serve   (default ".",     env OUTBOX_DIR)
  -addr         listen address                 (default ":8181", env OUTBOX_ADDR)
  -auto-reply   spawn the agent CLI in-process on each human comment (opt-in)
  -logs         print the auto-reply agent's output/thinking stream
                (default true, env OUTBOX_LOGS; -logs=false ⇒ lifecycle lines only)

When any project is registered (see "outbox add"), serve ignores -dir and serves
ALL registered projects; switch between them in the UI.

Examples:
  outbox serve                        # serve ./ on :8181
  outbox serve -dir docs              # serve the docs/ folder
  outbox serve -addr :9090            # listen on :9090
  outbox serve -auto-reply            # also run the in-process agent loop
  outbox serve -auto-reply -logs=false  # agent loop on, quiet (no output stream)
`,
	"up": `outbox up — serve, then open the browser at the review UI.

Usage:
  outbox up [-dir <folder>] [-addr <host:port>] [-auto-reply] [-logs=<bool>]

Same flags as "outbox serve". "up" also does a best-effort self-update before
binding the port (disable with auto_update: false / OUTBOX_AUTO_UPDATE=false).

Examples:
  outbox up                           # serve ./ and open the browser
  outbox up -dir docs -addr :9090     # serve docs/ on :9090 and open it
  outbox up -auto-reply               # open the UI with the agent loop on
  outbox up -logs=false               # start quietly (no agent output stream)
`,
	"init": `outbox init — scaffold outbox.yaml + register the MCP with detected AI clients.

Usage:
  outbox init [-dir <folder>] [-addr <host:port>] [-client <name>]... [-all]

Flags:
  -dir       folder to scaffold              (default ".",     env OUTBOX_DIR)
  -addr      listen address for the MCP URL  (default ":8181", env OUTBOX_ADDR)
  -client    register only this client (repeatable): claude-code, gemini, cursor,
             windsurf, claude-desktop, codex
  -all       register with every supported client, even if not detected

Examples:
  outbox init                         # scaffold ./outbox.yaml, auto-detect clients
  outbox init -dir docs               # scaffold docs/outbox.yaml
  outbox init -client claude-code     # register only Claude Code
  outbox init -all                    # register every supported client
`,
	"add": `outbox add — register a project: a repo root plus one or more docs subpaths.

Usage:
  outbox add <root> <docs...> [--agent <preset> | --agent-cmd <cmd>]

  <root>        project repo root (required, must be an existing directory)
  <docs...>     ONE OR MORE spec subpaths under <root> (required). Use "." for
                the whole repo. Each must be an existing directory under <root>.
  --agent       per-project agent preset: claude, codex, copilot
  --agent-cmd   per-project agent command with a {prompt} token (overrides --agent)

A project serves the UNION of its docs subpaths; docs are keyed relative to the
root, so the same filename under two subpaths never collides. Auto-reply spawns
the agent with cwd = the root (so it sees the repo's CLAUDE.md/.mcp.json/codebase).

Examples:
  outbox add ~/my-specs-repo .        # whole repo is specs
  outbox add ~/work/app docs/specs    # serve only the docs/specs subpath
  outbox add ~/work/app specs api-specs   # serve TWO subpaths as one project
  outbox add ~/work/api . --agent codex   # this project's auto-reply uses codex
`,
	"remove": `outbox remove — unregister projects, or individual docs subpaths.

Usage:
  outbox remove                       # interactive multiselect (docs granularity)
  outbox remove <name|root>           # remove the WHOLE matching project

With no argument, remove lists every project AND each of its docs entries as a
tickable row; space toggles, enter confirms, q/esc cancels. Removing a project's
LAST docs entry drops the project entirely. (Needs a terminal — pass a name to
remove non-interactively.)

Examples:
  outbox remove                       # pick docs/projects to remove from a list
  outbox remove app                   # remove the whole project named "app"
  outbox remove ~/work/app            # remove the whole project rooted there
`,
	"retry": `outbox retry — re-queue stranded (claimed-but-unfinished) comments back to open.

Usage:
  outbox retry [-dir <folder>] [project]

Resets every 'claimed' comment back to 'open' (clearing its claim), so it
re-enters the agent work set. A running server picks the re-queued comments up on
its next trigger or startup sweep; a stopped server processes them on next boot.
It operates on the review database directly, so it works whether or not the
server is running.

  (no arg)        re-queue ALL registered projects (single-folder mode: the served dir)
  <project>       re-queue only that registered project (multi-project mode)
  -dir <folder>   single-folder mode: locate the DB under this served dir, matching
                  serve/up (flag > OUTBOX_DIR > "."; default "."). Needed when the
                  server was started with 'outbox up -dir <folder>'.

Examples:
  outbox retry                        # re-queue every project's stranded comments
  outbox retry app                    # re-queue only the project named "app"
  outbox retry -dir docs              # single-folder DB started with 'outbox up -dir docs'
`,
	"list": `outbox list — list registered projects (alias: outbox projects).

Usage:
  outbox list

Prints one line per project: name, its root/docs location(s), and [agent] if set.

Examples:
  outbox list                         # show every registered project
  outbox projects                     # same thing (alias)
`,
	"upgrade": `outbox upgrade — update outbox to the latest release (self-update).

Usage:
  outbox upgrade

Downloads and replaces the running binary with the latest GitHub release.
Homebrew installs should instead run "brew update && brew upgrade outbox-md";
Docker updates via image pull / Watchtower.

Examples:
  outbox upgrade                      # self-update to the latest release
`,
	"paths": `outbox paths — print the resolved on-disk locations outbox uses.

Usage:
  outbox paths

Prints, labelled and one per line, where the registry, the review database, and
the outbox.yaml config live for the CURRENT mode (single-dir vs multi-project),
so you can cd/open them.

Examples:
  outbox paths                        # show registry / db / config locations
`,
	"settings": `outbox settings — view or change the structured fields of ./outbox.yaml.

Usage:
  outbox settings                     # interactive walkthrough (Enter keeps current)
  outbox settings <key> <value>       # set one field directly

Structured fields (bool): auto_update, auto_reply. Free-text fields (agent_cmd,
sources) are shown read-only — edit those in outbox.yaml. Operates on ./outbox.yaml
in the current directory; run "outbox init" first if it does not exist.

Examples:
  outbox settings                     # walk through each field
  outbox settings auto_reply true     # turn the auto-reply loop on
  outbox settings auto_update false   # opt out of self-update
`,
	"version": `outbox version — print the version.

Usage:
  outbox version

Examples:
  outbox version                      # print the installed version
`,
}

// usageFor writes the per-command usage+examples for name, falling back to the
// top-level usage when name has no dedicated help.
func usageFor(w io.Writer, name string) {
	if u, ok := commandUsage[name]; ok {
		fmt.Fprint(w, u)
		return
	}
	usage(w)
}

// usage writes the top-level help: what outbox is, the command list (each with a
// one-line example), and how to get started. Bare `outbox` (no subcommand) prints
// this rather than starting a server.
func usage(w io.Writer) {
	fmt.Fprint(w, `outbox-md — local-first, agent-agnostic review for AI-generated Markdown specs.

Usage:
  outbox <command> [flags]

Commands (with a one-line example each):
  serve      Serve the review UI + MCP endpoint      outbox serve -dir docs
  up         Serve, then open the browser            outbox up
  init       Scaffold outbox.yaml + register MCP     outbox init
  add        Register a project (root + docs...)     outbox add ~/work/app docs/specs
  remove     Unregister projects/docs (multiselect)  outbox remove
  list       List registered projects (a: projects) outbox list
  retry      Re-queue stranded claimed comments      outbox retry
  paths      Print outbox's on-disk locations        outbox paths
  settings   View/change outbox.yaml fields          outbox settings auto_reply true
  upgrade    Self-update to the latest release       outbox upgrade
  version    Print the version                       outbox version
  help       Show this help (or "help <command>")    outbox help add

Getting started:
  outbox init && outbox up

Multiple projects:
  outbox add ~/my-specs-repo .        # register a repo (whole repo is specs)
  outbox add ~/work/app docs/specs    # register only a subpath
  outbox up                           # serve ALL registered projects; switch in the UI

Run "outbox help <command>" for a command's flags and examples.
`)
}

// helpCommand implements `outbox help [command]`: with no argument it prints the
// top-level usage; with one it prints that command's usage+examples (or, for an
// unknown name, the top-level usage). It never fails.
func helpCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		usage(out)
		return nil
	}
	name := args[0]
	if _, ok := commandUsage[name]; ok || name == "help" {
		usageFor(out, name)
		return nil
	}
	// Unknown topic: show the top-level help so the user sees the valid commands.
	usage(out)
	return nil
}

// commandNames returns the sorted set of documented command names (used by tests
// to assert every command carries an EXAMPLES section).
func commandNames() []string {
	names := make([]string, 0, len(commandUsage))
	for n := range commandUsage {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

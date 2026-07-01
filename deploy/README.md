# Deploying & operating outbox-md

The turnkey ops layer: run the server, start a webhook runner, and keep it alive
across reboots — with **one command** for the common path and a **service unit**
for the persistent path. It wraps the published server image
([`docker-compose.yml`](../docker-compose.yml)) and the reference runners
([`examples/runner/`](../examples/runner/README.md)); nothing here duplicates
runner code.

Two ways to run the runner:

| Path | Use when | How |
|---|---|---|
| **Foreground / detached** (dev) | trying it out, iterating | `make runner` (or run the runner directly) |
| **Service unit** (persistent) | keep it alive across reboots | launchd (macOS) / systemd (Linux) |

> `make` targets use `lsof` — macOS/Linux only. On Windows, use WSL or start the
> runner directly (`python3 examples/runner/python/main.py`).

---

## 1. Run the server (published image)

From the repo root:

```bash
make up          # docker compose up -d  → http://localhost:8181
make status      # containers + whether the runner port is listening
make down        # stop
make pull        # fetch the latest image and recreate
```

`make up` is idempotent. Point it at your specs folder with `OUTBOX_DIR`:

```bash
OUTBOX_DIR=path/to/your/specs make up
```

## 2. Register the MCP (cli-mode runner)

The default `cli` runner spawns your coding-agent CLI, which writes back through
MCP. Register the server with the CLI once:

```bash
claude mcp add --transport http outbox-md http://localhost:8181/mcp
claude mcp list   # outbox-md ✓ connected
```

## 3. Start the runner

```bash
make runner                              # python (default), port 8787
make runner RUNNER_LANG=go               # or: go / node
make runner RUNNER_PORT=9000 OUTBOX_WEBHOOK_SECRET=your-shared-secret
make logs                                # tail runner.log
```

`make runner` frees the port if something is already bound, launches the runner
**detached** into `runner.log`, then polls briefly and reports bind success or
prints the last log lines on failure. Overridable variables (`make` for the
menu): `RUNNER_PORT`, `RUNNER_LANG`, `OUTBOX_WEBHOOK_SECRET`, `RUNNER_ADDR`,
`RUNNER_AGENT_CMD`.

## 4. Wire the server → runner

The server pushes a webhook to the runner on each governance event. Set the
runner URL and a **shared secret** (must match on both sides), then bring the
server up:

```bash
OUTBOX_WEBHOOK_URL=http://host.docker.internal:8787/ \
OUTBOX_WEBHOOK_SECRET=your-shared-secret \
OUTBOX_DIR=path/to/your/specs docker compose up -d
```

- The server runs in a container; the runner runs on the host — so the server
  reaches it at **`host.docker.internal`**, not `localhost`. (Same-network
  containerized runner? Use its compose service name instead.)
- `OUTBOX_WEBHOOK_SECRET` must be **identical** for the server and the runner —
  it's the HMAC signing key. With no secret the runner is default-deny (see the
  [runner README](../examples/runner/README.md#the-hmac--event-contract)).

Now comment in the browser → server POSTs the runner → your agent proposes /
replies over MCP. No polling.

---

## 5. Keep the runner alive (service units)

For a hands-off setup, install the runner as a **user** service so it restarts
on crash and comes back after a reboot. Both units are **templates** with
clearly marked `__PLACEHOLDER__`s and **no absolute paths baked in** — edit a
copy, don't install in place.

Run them **as your user, not root/sudo**, so the runner inherits your CLI login
(`~/.claude`, keychain). A root service would run the agent with no auth.

### macOS — launchd

Template: [`launchd/com.outbox-md.runner.plist`](launchd/com.outbox-md.runner.plist)

```bash
cp deploy/launchd/com.outbox-md.runner.plist ~/Library/LaunchAgents/
# edit the copy: set __USER__, __REPO_DIR__, __SECRET__
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.outbox-md.runner.plist
launchctl enable  gui/$(id -u)/com.outbox-md.runner
launchctl kickstart -k gui/$(id -u)/com.outbox-md.runner   # (re)start now
# stop/remove:
launchctl bootout gui/$(id -u)/com.outbox-md.runner
```

### Linux — systemd (`--user`)

Template: [`systemd/outbox-md-runner.service`](systemd/outbox-md-runner.service)

```bash
mkdir -p ~/.config/systemd/user ~/.config/outbox-md
cp deploy/systemd/outbox-md-runner.service ~/.config/systemd/user/
# edit the copy: set __REPO_DIR__
printf 'OUTBOX_WEBHOOK_SECRET=change-me\nRUNNER_ADDR=:8787\n' > ~/.config/outbox-md/runner.env
systemctl --user daemon-reload
systemctl --user enable --now outbox-md-runner
journalctl --user -u outbox-md-runner -f
# survive reboot with no active login session:
sudo loginctl enable-linger $(whoami)
```

Both units default to the **python** runner; each file has a commented one-liner
to swap in the go or node runner.

---

## 6. Enterprise / proxy extension point

Some networks require wrapping the agent invocation — a corporate HTTP proxy, or
non-default LLM auth. There is **no vendor code here**; the seam is a single
env var. Keep the default clean:

```
claude -p {prompt} --allowedTools mcp__outbox-md__*
```

To wrap it, override **`RUNNER_AGENT_CMD`** so the `claude` (or other CLI) call
runs inside your proxy wrapper — `{prompt}` is substituted as one argument, no
shell:

```bash
make runner RUNNER_AGENT_CMD='your-proxy-wrapper claude -p {prompt} --allowedTools mcp__outbox-md__*'
```

In a service unit, set `RUNNER_AGENT_CMD` in the plist's `EnvironmentVariables`
(macOS) or the `runner.env` file (Linux) — or wrap the whole `ExecStart` /
`ProgramArguments` launch. That's the only hook; everything else stays generic.

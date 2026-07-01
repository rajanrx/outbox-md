"""Core runner: HMAC verify -> event filter -> debounce/single-flight -> agent run.

Stdlib only (hmac, hashlib, threading, http.server, json).
"""

import hashlib
import hmac
import json
import sys
import threading
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def verify_signature(secret: str, allow_unsigned: bool, body: bytes, header: str) -> bool:
    """Report whether the request is authentic.

    - secret set, header valid -> accept; header missing/wrong -> reject.
    - secret falsy -> default-deny: reject unless allow_unsigned is set (an
      explicit opt-in for the unsigned, no-secret configuration).

    `body` MUST be the raw request bytes — the exact bytes the server signed.
    """
    if not secret:
        return allow_unsigned
    if not header:
        return False
    got = header[len("sha256="):] if header.startswith("sha256=") else header
    want = hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest()
    # compare_digest is constant-time and length-safe. It raises TypeError on a
    # non-ASCII header (attacker-controlled); treat that as a clean reject rather
    # than a noisy 500, matching Go/Node's fail-closed behavior.
    try:
        return hmac.compare_digest(got, want)
    except TypeError:
        return False


def event_name(header: str, body: bytes) -> str:
    """Prefer the X-Outbox-Event header, fall back to the body's 'event' field."""
    if header:
        return header
    try:
        return json.loads(body.decode("utf-8")).get("event", "")
    except (ValueError, UnicodeDecodeError):
        return ""


def ack_received(cfg, event: str, body: bytes):
    """Best-effort, fire-and-forget POST to the server's untokened /received endpoint.

    For a comment.created/comment.replied event it tells the server the webhook
    landed so the human's "AI processing…" badge appears within ~1s — even if the
    agent later dies before it claims. It runs in a daemon thread with a short
    timeout; any failure is logged at most and never blocks or fails webhook
    handling. No-op when the event carries no comment, no server URL is
    configured, or the payload has no comment id.
    """
    if event not in ("comment.created", "comment.replied"):
        return
    if not cfg.server_url:
        return
    try:
        comment_id = json.loads(body.decode("utf-8")).get("commentId", "")
    except (ValueError, UnicodeDecodeError, AttributeError):
        comment_id = ""
    if not comment_id:
        return
    url = cfg.server_url.rstrip("/") + "/api/comments/" + comment_id + "/received"

    def _post():
        try:
            req = urllib.request.Request(url, data=b"", method="POST")
            with urllib.request.urlopen(req, timeout=3):  # noqa: S310 - operator-configured URL
                pass
        except Exception as e:  # noqa: BLE001 - ack failure must never propagate
            print(f"runner: received-ack failed: {e}", file=sys.stderr)

    threading.Thread(target=_post, daemon=True).start()


class Runner:
    """Debounce triggers and serialize agent runs.

    A burst within the debounce window collapses into one run; a trigger that
    arrives while a run is in flight schedules exactly one rerun afterwards
    (never two concurrent runs).
    """

    def __init__(self, debounce_ms: int, run):
        self.debounce = debounce_ms / 1000.0
        self.run = run  # callable () -> None
        self._lock = threading.Lock()
        self._timer = None
        self._running = False
        self._pending = False

    def trigger(self):
        """(Re)arm the debounce timer; repeated calls coalesce."""
        with self._lock:
            if self._timer is not None:
                self._timer.cancel()
            self._timer = threading.Timer(self.debounce, self._fire)
            self._timer.daemon = True
            self._timer.start()

    def _fire(self):
        threading.Thread(target=self.execute, daemon=True).start()

    def execute(self):
        """Run the agent with single-flight semantics.

        If a run is already in progress, set ``pending`` and return; the
        in-flight loop drains pending and runs once more.
        """
        with self._lock:
            if self._running:
                self._pending = True
                return
            self._running = True
        while True:
            try:
                self.run()
            except Exception as e:  # noqa: BLE001 - one bad run must not kill the loop
                print(f"runner: agent run failed: {e}", file=sys.stderr)
            with self._lock:
                if self._pending:
                    self._pending = False
                    continue
                self._running = False
                return


class _Handler(BaseHTTPRequestHandler):
    # cfg and runner are attached to the server instance by create_server.
    def _respond(self, code: int, text: str):
        self.send_response(code)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(text.encode("utf-8"))

    def do_GET(self):  # noqa: N802 - BaseHTTPRequestHandler API
        if self.path == "/healthz":
            self._respond(200, "ok\n")
        else:
            self._respond(404, "not found\n")

    def do_POST(self):  # noqa: N802 - BaseHTTPRequestHandler API
        cfg = self.server.cfg
        runner = self.server.runner
        # Cap the raw body BEFORE auth so a large body cannot OOM the process
        # pre-verification. Reject an oversized Content-Length outright, and cap
        # the actual read in case the header lies. HMAC still covers the bytes read.
        try:
            length = int(self.headers.get("Content-Length", 0) or 0)
        except ValueError:
            length = 0
        if length > cfg.max_body_bytes:
            self._respond(413, "request body too large\n")
            return
        body = self.rfile.read(min(length, cfg.max_body_bytes)) if length else b""
        if not verify_signature(cfg.secret, cfg.allow_unsigned, body, self.headers.get("X-Outbox-Signature", "")):
            self._respond(401, "invalid signature\n")
            return
        event = event_name(self.headers.get("X-Outbox-Event", ""), body)
        if event not in cfg.events:
            self._respond(200, "ignored\n")
            return
        # Acknowledge receipt to the server BEFORE the debounced spawn so the
        # human's "AI processing…" badge appears within ~1s — even if the agent
        # later dies before it claims. Best-effort, non-blocking, non-fatal.
        ack_received(cfg, event, body)
        runner.trigger()
        self._respond(202, "accepted\n")

    def log_message(self, *_args):  # silence default per-request stderr logging
        pass


def create_server(cfg, backend, host: str = "", port: int = 8787) -> ThreadingHTTPServer:
    """Build the HTTP server (webhook at '/', health at '/healthz')."""

    def run():
        print(f"runner: invoking agent (mode={cfg.agent_mode})")
        backend.run()
        print("runner: agent run complete")

    runner = Runner(cfg.debounce_ms, run)
    server = ThreadingHTTPServer((host, port), _Handler)
    server.cfg = cfg
    server.runner = runner
    return server

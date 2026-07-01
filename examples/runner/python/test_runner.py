"""Tests for the Python runner. Run: python -m unittest  (stdlib only)."""

import hashlib
import hmac
import os
import threading
import time
import unittest
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock

from cli import build_args
from config import Config, load_config, parse_events
from runner import Runner, create_server, event_name, verify_signature


def sign(secret, body):
    return "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()


class VerifySignatureTest(unittest.TestCase):
    def test_cases(self):
        body = b'{"event":"comment.created"}'
        secret = "shh"
        good = sign(secret, body)
        self.assertTrue(verify_signature(secret, False, body, good), "valid passes")
        self.assertFalse(verify_signature(secret, False, b'{"event":"x"}', good), "tampered fails")
        self.assertFalse(verify_signature(secret, False, body, "sha256=deadbeef"), "wrong sig fails")
        self.assertFalse(verify_signature(secret, False, body, ""), "missing sig fails")
        self.assertFalse(verify_signature("", False, body, ""), "no secret default-denies")
        self.assertFalse(verify_signature("", False, body, "sha256=whatever"), "no secret default-denies even with a header")
        self.assertTrue(verify_signature("", True, body, ""), "no secret + allow-unsigned accepts")
        self.assertTrue(verify_signature("", True, body, "sha256=whatever"), "no secret + allow-unsigned ignores header")

    def test_non_ascii_signature_header_rejects_cleanly(self):
        body = b'{"event":"comment.created"}'
        # A non-ASCII X-Outbox-Signature must fail closed (False), not raise.
        self.assertFalse(verify_signature("shh", False, body, "sha256=café—" + "ÿ"))


class EventNameTest(unittest.TestCase):
    def test_header_preferred_then_body(self):
        self.assertEqual(event_name("comment.created", b"{}"), "comment.created")
        self.assertEqual(event_name("", b'{"event":"comment.replied"}'), "comment.replied")
        self.assertEqual(event_name("", b"not json"), "")


class BuildArgsTest(unittest.TestCase):
    def test_prompt_is_single_arg(self):
        args = build_args("claude -p {prompt} --allowedTools mcp__outbox-md__*", "do the thing now")
        self.assertEqual(args, ["claude", "-p", "do the thing now", "--allowedTools", "mcp__outbox-md__*"])


class _CountingBackend:
    def __init__(self):
        self.calls = 0
        self._lock = threading.Lock()

    def run(self):
        with self._lock:
            self.calls += 1


class HTTPFilteringTest(unittest.TestCase):
    def test_filtering_and_signing_end_to_end(self):
        cfg = Config(
            secret="shh",
            events=parse_events("comment.created,comment.replied"),
            debounce_ms=5,
        )
        backend = _CountingBackend()
        server = create_server(cfg, backend, "127.0.0.1", 0)
        port = server.server_address[1]
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{port}/"

        def post(event, body, signature=None):
            headers = {"X-Outbox-Event": event, "Content-Type": "application/json"}
            if signature is not None:
                headers["X-Outbox-Signature"] = signature
            req = urllib.request.Request(base, data=body, headers=headers, method="POST")
            try:
                with urllib.request.urlopen(req) as resp:
                    return resp.status
            except urllib.error.HTTPError as e:
                e.close()
                return e.code

        try:
            body = b'{"event":"comment.created"}'
            # allowed + correctly signed -> 202
            self.assertEqual(post("comment.created", body, sign("shh", body)), 202)
            # bad signature -> 401
            self.assertEqual(post("comment.created", body, "sha256=bad"), 401)
            # filtered event -> 200 ignore
            rbody = b'{"event":"comment.resolved"}'
            self.assertEqual(post("comment.resolved", rbody, sign("shh", rbody)), 200)

            time.sleep(0.05)
            self.assertEqual(backend.calls, 1, "only the allowed, signed event ran the agent")
        finally:
            server.shutdown()


class AuthPolicyHTTPTest(unittest.TestCase):
    """Default-deny, allow-unsigned opt-in, and the pre-auth body cap, over HTTP."""

    def _serve(self, cfg):
        backend = _CountingBackend()
        server = create_server(cfg, backend, "127.0.0.1", 0)
        port = server.server_address[1]
        threading.Thread(target=server.serve_forever, daemon=True).start()
        return server, backend, f"http://127.0.0.1:{port}/"

    def _post(self, base, event, body):
        headers = {"X-Outbox-Event": event, "Content-Type": "application/json"}
        req = urllib.request.Request(base, data=body, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(req) as resp:
                return resp.status
        except urllib.error.HTTPError as e:
            e.close()
            return e.code

    def test_default_deny_unsigned(self):
        cfg = Config(secret="", allow_unsigned=False, events=parse_events("comment.created"), debounce_ms=5)
        server, backend, base = self._serve(cfg)
        try:
            self.assertEqual(self._post(base, "comment.created", b'{"event":"comment.created"}'), 401)
            time.sleep(0.03)
            self.assertEqual(backend.calls, 0, "default-deny rejects unsigned")
        finally:
            server.shutdown()

    def test_allow_unsigned_accepts(self):
        cfg = Config(secret="", allow_unsigned=True, events=parse_events("comment.created"), debounce_ms=5)
        server, backend, base = self._serve(cfg)
        try:
            self.assertEqual(self._post(base, "comment.created", b'{"event":"comment.created"}'), 202)
            time.sleep(0.05)
            self.assertEqual(backend.calls, 1, "unsigned accepted under explicit opt-in")
        finally:
            server.shutdown()

    def test_oversize_body_rejected(self):
        cfg = Config(secret="", allow_unsigned=True, max_body_bytes=16, events=parse_events("comment.created"), debounce_ms=5)
        server, backend, base = self._serve(cfg)
        try:
            big = b'{"event":"comment.created","pad":"' + b"x" * 4096 + b'"}'
            self.assertEqual(self._post(base, "comment.created", big), 413)
            time.sleep(0.03)
            self.assertEqual(backend.calls, 0, "oversize body rejected before run")
        finally:
            server.shutdown()


class _AckStubHandler(BaseHTTPRequestHandler):
    def do_POST(self):  # noqa: N802 - BaseHTTPRequestHandler API
        length = int(self.headers.get("Content-Length", 0) or 0)
        if length:
            self.rfile.read(length)
        if self.path.endswith("/received"):
            self.server.ack_hits.append(self.path)
            self.server.ack_event.set()
        self.send_response(200)
        self.end_headers()

    def log_message(self, *_args):
        pass


class ReceivedAckTest(unittest.TestCase):
    def test_ack_posted_for_signed_comment_created(self):
        stub = ThreadingHTTPServer(("127.0.0.1", 0), _AckStubHandler)
        stub.ack_hits = []
        stub.ack_event = threading.Event()
        threading.Thread(target=stub.serve_forever, daemon=True).start()
        stub_url = f"http://127.0.0.1:{stub.server_address[1]}"

        cfg = Config(secret="shh", events=parse_events("comment.created,comment.replied"), debounce_ms=5, server_url=stub_url)
        backend = _CountingBackend()
        server = create_server(cfg, backend, "127.0.0.1", 0)
        port = server.server_address[1]
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{port}/"
        body = b'{"event":"comment.created","commentId":"cmt-123"}'
        headers = {"X-Outbox-Event": "comment.created", "X-Outbox-Signature": sign("shh", body)}
        try:
            req = urllib.request.Request(base, data=body, headers=headers, method="POST")
            with urllib.request.urlopen(req) as resp:
                self.assertEqual(resp.status, 202)
            self.assertTrue(stub.ack_event.wait(2.0), "expected a POST to /received")
            self.assertEqual(stub.ack_hits, ["/api/comments/cmt-123/received"])
            time.sleep(0.05)
            self.assertEqual(backend.calls, 1, "agent still runs after the ack")
        finally:
            server.shutdown()
            stub.shutdown()

    def test_ack_failure_is_non_fatal(self):
        # server_url points at a closed port → connection refused fast; the webhook
        # must still succeed and the agent must still run.
        cfg = Config(secret="", allow_unsigned=True, events=parse_events("comment.created"), debounce_ms=5, server_url="http://127.0.0.1:1")
        backend = _CountingBackend()
        server = create_server(cfg, backend, "127.0.0.1", 0)
        port = server.server_address[1]
        threading.Thread(target=server.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{port}/"
        body = b'{"event":"comment.created","commentId":"cmt-9"}'
        try:
            req = urllib.request.Request(base, data=body, headers={"X-Outbox-Event": "comment.created"}, method="POST")
            with urllib.request.urlopen(req) as resp:
                self.assertEqual(resp.status, 202)
            time.sleep(0.05)
            self.assertEqual(backend.calls, 1, "ack failure must not block the run")
        finally:
            server.shutdown()

    def test_server_url_default_strips_mcp_suffix(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertEqual(load_config().server_url, "http://localhost:8181")


class DebounceTest(unittest.TestCase):
    def test_burst_coalesces_to_one_run(self):
        calls = []
        r = Runner(30, lambda: calls.append(1))
        for _ in range(10):
            r.trigger()
            time.sleep(0.002)
        time.sleep(0.08)
        self.assertEqual(len(calls), 1)


class SingleFlightTest(unittest.TestCase):
    def test_triggers_during_run_coalesce_to_one_rerun(self):
        calls = []
        release = threading.Event()
        started = threading.Event()

        def run():
            calls.append(1)
            if len(calls) == 1:
                started.set()
                release.wait(2.0)  # block run #1 so we can pile up triggers

        r = Runner(1, run)
        worker = threading.Thread(target=r.execute)
        worker.start()
        self.assertTrue(started.wait(2.0))

        for _ in range(5):
            r.execute()  # all coalesce into one pending rerun
        release.set()
        worker.join(2.0)

        self.assertEqual(len(calls), 2, "initial run + exactly one coalesced rerun")


if __name__ == "__main__":
    unittest.main()

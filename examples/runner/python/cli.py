"""cli-mode backend: spawn a headless coding-agent CLI (Claude Code by default).

The cost-efficient default — uses the user's existing CLI subscription, so no
per-token API cost and no API key in the runner. The CLI must have the
outbox-md MCP configured (see the README: `claude mcp add ...`).
"""

import subprocess


def build_args(template: str, prompt: str):
    """Tokenize the command template and substitute {prompt} as ONE argv element.

    The command is run without a shell (list form), so the multi-word prompt
    stays a single argument (no injection surface) and glob tokens like
    ``mcp__outbox-md__*`` are passed through literally.
    """
    return [prompt if f == "{prompt}" else f for f in template.split()]


class CLIBackend:
    def __init__(self, cmd_template: str, prompt: str, timeout: float = 600.0):
        self.cmd_template = cmd_template
        self.prompt = prompt
        self.timeout = timeout

    def run(self):
        args = build_args(self.cmd_template, self.prompt)
        if not args:
            raise RuntimeError("cli: empty command template")
        proc = subprocess.run(  # noqa: S603 - args come from config, run without a shell
            args,
            capture_output=True,
            text=True,
            timeout=self.timeout,
        )
        out = (proc.stdout or "") + (proc.stderr or "")
        if out.strip():
            print(f"cli: {args[0]} output:\n{out.rstrip()}")
        if proc.returncode != 0:
            raise RuntimeError(f"cli: {args[0]} exited with code {proc.returncode}")

// cli-mode backend: spawn a headless coding-agent CLI (Claude Code by default).
// The cost-efficient default — uses the user's existing CLI subscription, so no
// per-token API cost and no API key in the runner. The CLI must have the
// outbox-md MCP configured (see the README: `claude mcp add ...`).
import { spawn } from "node:child_process";

// buildArgs tokenizes the command template on whitespace and substitutes the
// instruction prompt for the literal {prompt} token as a SINGLE argv element.
// The command is spawned without a shell, so the multi-word prompt stays one
// argument (no injection surface) and glob tokens like mcp__outbox-md__* are
// passed through literally.
export function buildArgs(template, prompt) {
  return template
    .split(/\s+/)
    .filter(Boolean)
    .map((f) => (f === "{prompt}" ? prompt : f));
}

export class CLIBackend {
  constructor(cmdTemplate, prompt) {
    this.cmdTemplate = cmdTemplate;
    this.prompt = prompt;
  }

  run() {
    return new Promise((resolve, reject) => {
      const args = buildArgs(this.cmdTemplate, this.prompt);
      if (args.length === 0) {
        reject(new Error("cli: empty command template"));
        return;
      }
      const [cmd, ...rest] = args;
      const child = spawn(cmd, rest, { stdio: ["ignore", "pipe", "pipe"] });
      let out = "";
      child.stdout.on("data", (d) => (out += d));
      child.stderr.on("data", (d) => (out += d));
      child.on("error", reject);
      child.on("close", (code) => {
        if (out) console.log(`cli: ${cmd} output:\n${out.replace(/\n+$/, "")}`);
        if (code === 0) resolve();
        else reject(new Error(`cli: ${cmd} exited with code ${code}`));
      });
    });
  }
}

package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// CLIBackend drives a headless coding-agent CLI (Claude Code by default). It is
// the cost-efficient default: the CLI uses the user's existing subscription, so
// there is no per-token API cost and no API key in the runner. The CLI must have
// the outbox-md MCP configured (see the README: `claude mcp add ...`).
type CLIBackend struct {
	CmdTemplate string
	Prompt      string
	Timeout     time.Duration
}

// buildArgs tokenizes the command template on whitespace and substitutes the
// instruction prompt for the literal {prompt} token as a SINGLE argv element.
// The command is exec'd directly (no shell), so the multi-word prompt stays one
// argument and there is no shell-injection surface; glob tokens such as
// mcp__outbox-md__* are passed through literally.
func buildArgs(template, prompt string) []string {
	fields := strings.Fields(template)
	args := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "{prompt}" {
			args = append(args, prompt)
			continue
		}
		args = append(args, f)
	}
	return args
}

// Run invokes the CLI once and logs its combined output and exit status.
func (b *CLIBackend) Run() error {
	args := buildArgs(b.CmdTemplate, b.Prompt)
	if len(args) == 0 {
		return fmt.Errorf("cli: empty command template")
	}
	ctx := context.Background()
	if b.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("cli: %s output:\n%s", args[0], strings.TrimRight(string(out), "\n"))
	}
	if err != nil {
		return fmt.Errorf("cli: %s: %w", args[0], err)
	}
	return nil
}

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rajanrx/outbox-md/internal/registry"
)

// askKBTimeout bounds a single Ask-the-KB LLM spawn. This is an interactive Q&A
// path (a human is waiting on the answer), so it is deliberately far shorter than
// the auto-reply engine's 15m run cap.
const askKBTimeout = 120 * time.Second

// newKBLLMSpawn builds the Ask-the-KB LLM-spawn seam. It resolves the project's LLM
// command — the project's CHAIR when it is a council project, else its AgentCmd(),
// else the engine default (defaultAgentCmd) — and runs it as a PLAIN completion:
// the grounded prompt is substituted for the {prompt} token as a single argv
// element (no shell), and stdout is captured as the answer.
//
// It deliberately does NOT run in the project root: spawning there would load that
// repo's .mcp.json and attach the outbox governance tools, pointing the Q&A agent
// back at this server. This is Q&A, not the review loop, so it runs in a neutral
// working directory (os.TempDir) where no project .mcp.json is present — the
// --allowedTools mcp__outbox-md__* flag in the default template then matches nothing
// and is a harmless no-op. A spawn/timeout error is returned (never a panic), with
// stderr folded into the message so the API can surface why the model failed.
func newKBLLMSpawn(projects []registry.Project, defaultAgentCmd string) func(project, prompt string) (string, error) {
	byName := make(map[string]registry.Project, len(projects))
	for _, p := range projects {
		byName[p.Name] = p
	}
	return func(project, prompt string) (string, error) {
		cmdTemplate := defaultAgentCmd
		if p, ok := byName[project]; ok {
			switch {
			case p.IsCouncil() && strings.TrimSpace(p.ChairCmd()) != "":
				cmdTemplate = p.ChairCmd()
			case strings.TrimSpace(p.AgentCmd()) != "":
				cmdTemplate = p.AgentCmd()
			}
		}
		args := substitutePrompt(cmdTemplate, prompt)
		if len(args) == 0 {
			return "", fmt.Errorf("no LLM command configured for project %q", project)
		}
		ctx, cancel := context.WithTimeout(context.Background(), askKBTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		// Neutral cwd: no project .mcp.json ⇒ no outbox governance tools attached.
		cmd.Dir = os.TempDir()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return "", fmt.Errorf("LLM timed out after %s", askKBTimeout)
			}
			detail := strings.TrimSpace(stderr.String())
			if detail != "" {
				return "", fmt.Errorf("LLM spawn failed: %v: %s", err, detail)
			}
			return "", fmt.Errorf("LLM spawn failed: %w", err)
		}
		return string(out), nil
	}
}

// substitutePrompt tokenizes the command template on whitespace and replaces the
// literal {prompt} token with prompt as ONE argv element (mirrors autoreply's
// buildArgs). Exec'd directly (no shell), so the multi-word prompt stays a single
// argument with no shell-injection surface.
func substitutePrompt(template, prompt string) []string {
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

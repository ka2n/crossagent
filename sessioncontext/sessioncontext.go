// Package sessioncontext collects best-effort facts at a hook's observation time.
package sessioncontext

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ka2n/crossagent/agent"
)

type Snapshot struct {
	Cwd        string    `json:"cwd"`
	ObservedAt time.Time `json:"observed_at"`
	Git        *Git      `json:"git,omitempty"`
	Terminal   *Terminal `json:"terminal,omitempty"`
}

type Git struct {
	WorktreeRoot string `json:"worktree_root"`
	Branch       string `json:"branch,omitempty"`
	HEAD         string `json:"head,omitempty"`
	Detached     bool   `json:"detached,omitempty"`
	Source       string `json:"source"`
}

type Terminal struct {
	Title  string `json:"title"`
	Source string `json:"source"`
}

// CommandRunner receives an executable, arguments and the complete child environment.
// Implementations must honor cancellation of ctx.
type CommandRunner func(ctx context.Context, executable string, args, env []string) ([]byte, error)

type Options struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Run      CommandRunner
	Now      func() time.Time
	Timeout  time.Duration
	Agent    agent.Name
	// SessionID requires a matching pane identity before accepting its title.
	SessionID string
}

// Collect returns partial facts on command failures. Timeout bounds all commands
// together; its default is 500ms. An empty cwd intentionally disables Git probing.
func Collect(ctx context.Context, cwd string, opts Options) Snapshot {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Run == nil {
		opts.Run = run
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 500 * time.Millisecond
	}
	snapshot := Snapshot{Cwd: cwd, ObservedAt: opts.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	env := gitEnvironment(os.Environ())
	if cwd != "" {
		if binary, err := opts.LookPath("git"); err == nil {
			query := func(args ...string) (string, error) {
				output, err := opts.Run(ctx, binary, append([]string{"-C", cwd}, args...), env)
				return strings.TrimSpace(string(output)), err
			}
			if root, err := query("rev-parse", "--show-toplevel"); err == nil && root != "" {
				facts := &Git{WorktreeRoot: root, Source: "git"}
				if branch, err := query("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
					facts.Branch = branch
				}
				if head, err := query("rev-parse", "--verify", "HEAD"); err == nil {
					facts.HEAD = head
				}
				// A readable raw HEAD distinguishes detached state from symbolic-ref failures.
				if facts.Branch == "" && facts.HEAD != "" {
					raw, err := query("rev-parse", "--symbolic-full-name", "HEAD")
					facts.Detached = err == nil && raw == "HEAD"
				}
				snapshot.Git = facts
			}
		}
	}
	if opts.Getenv("HERDR_ENV") == "1" && opts.Getenv("HERDR_PANE_ID") != "" {
		if binary, err := opts.LookPath("herdr"); err == nil {
			output, err := opts.Run(ctx, binary, []string{"pane", "current", "--current"}, os.Environ())
			if err == nil {
				snapshot.Terminal = parseTerminal(output, opts)
			}
		}
	}
	return snapshot
}

func parseTerminal(output []byte, opts Options) *Terminal {
	var response struct {
		Result struct {
			Pane struct {
				Title        string `json:"terminal_title"`
				Stripped     string `json:"terminal_title_stripped"`
				AgentSession *struct {
					Value string     `json:"value"`
					Agent agent.Name `json:"agent"`
				} `json:"agent_session"`
			} `json:"pane"`
		} `json:"result"`
	}
	if json.Unmarshal(output, &response) != nil {
		return nil
	}
	pane := response.Result.Pane
	if opts.SessionID != "" && (pane.AgentSession == nil || pane.AgentSession.Value != opts.SessionID || (opts.Agent != "" && pane.AgentSession.Agent != "" && pane.AgentSession.Agent != opts.Agent)) {
		return nil
	}
	title := pane.Stripped
	if title == "" {
		title = pane.Title
	}
	if title == "" {
		return nil
	}
	return &Terminal{Title: title, Source: "herdr"}
}

func run(ctx context.Context, executable string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = env
	cmd.WaitDelay = 20 * time.Millisecond
	return cmd.Output()
}

func gitEnvironment(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		// Hook-inherited repository overrides must not redirect a supplied cwd.
		if strings.HasPrefix(key, "GIT_") {
			continue
		}
		clean = append(clean, entry)
	}
	return clean
}

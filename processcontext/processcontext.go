// Package processcontext identifies the coding-agent session hosting the current
// process using environment supplied by that host or its lifecycle integration.
// It does not inspect hook input, discover other sessions, or probe processes.
package processcontext

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ka2n/crossagent/agent"
)

var (
	ErrUnavailable = errors.New("process session identity unavailable")
	ErrAmbiguous   = errors.New("process session identity ambiguous")
)

// Context is the agent and session hosting a process. Source identifies the
// environment variable pair or native variable that supplied it.
type Context struct {
	Agent     agent.Name `json:"agent"`
	SessionID string     `json:"session_id"`
	Source    string     `json:"source"`
}

type Options struct {
	Getenv func(string) string
}

// Resolve reads CROSSAGENT_AGENT/CROSSAGENT_SESSION_ID exported by an integration,
// CODEX_THREAD_ID, and CLAUDE_CODE_SESSION_ID. Incomplete or conflicting context
// is rejected rather than routing work to a potentially inherited parent session.
func Resolve(opts Options) (Context, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var candidates []Context
	host, id := strings.TrimSpace(getenv("CROSSAGENT_AGENT")), strings.TrimSpace(getenv("CROSSAGENT_SESSION_ID"))
	if host != "" || id != "" {
		if host == "" || id == "" {
			return Context{}, fmt.Errorf("%w: CROSSAGENT_AGENT and CROSSAGENT_SESSION_ID must be set together", ErrUnavailable)
		}
		name, err := agent.Parse(host)
		if err != nil {
			return Context{}, err
		}
		candidates = append(candidates, Context{Agent: name, SessionID: id, Source: "CROSSAGENT_AGENT/CROSSAGENT_SESSION_ID"})
	}
	for _, native := range []struct {
		key   string
		agent agent.Name
	}{{"CODEX_THREAD_ID", agent.Codex}, {"CLAUDE_CODE_SESSION_ID", agent.Claude}} {
		if id := strings.TrimSpace(getenv(native.key)); id != "" {
			candidates = append(candidates, Context{Agent: native.agent, SessionID: id, Source: native.key})
		}
	}
	if len(candidates) == 0 {
		return Context{}, ErrUnavailable
	}
	chosen := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Agent != chosen.Agent || candidate.SessionID != chosen.SessionID {
			return Context{}, ErrAmbiguous
		}
	}
	return chosen, nil
}

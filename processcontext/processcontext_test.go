package processcontext

import (
	"errors"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want Context
		err  error
	}{
		{name: "Codex child process", env: map[string]string{"CODEX_THREAD_ID": "thread"}, want: Context{agent.Codex, "thread", "CODEX_THREAD_ID"}},
		{name: "Claude child process", env: map[string]string{"CLAUDE_CODE_SESSION_ID": "session"}, want: Context{agent.Claude, "session", "CLAUDE_CODE_SESSION_ID"}},
		{name: "Pi integration", env: map[string]string{"CROSSAGENT_AGENT": "pi", "CROSSAGENT_SESSION_ID": "session"}, want: Context{agent.Pi, "session", "CROSSAGENT_AGENT/CROSSAGENT_SESSION_ID"}},
		{name: "matching native identity", env: map[string]string{"CROSSAGENT_AGENT": "codex", "CROSSAGENT_SESSION_ID": "thread", "CODEX_THREAD_ID": "thread"}, want: Context{agent.Codex, "thread", "CROSSAGENT_AGENT/CROSSAGENT_SESSION_ID"}},
		{name: "no host", err: ErrUnavailable},
		{name: "partial integration", env: map[string]string{"CROSSAGENT_AGENT": "pi"}, err: ErrUnavailable},
		{name: "two native hosts", env: map[string]string{"CODEX_THREAD_ID": "child", "CLAUDE_CODE_SESSION_ID": "parent"}, err: ErrAmbiguous},
		{name: "inherited Pi identity in Codex", env: map[string]string{"CROSSAGENT_AGENT": "pi", "CROSSAGENT_SESSION_ID": "parent", "CODEX_THREAD_ID": "child"}, err: ErrAmbiguous},
		{name: "inherited Codex in Pi", env: map[string]string{"CROSSAGENT_AGENT": "pi", "CROSSAGENT_SESSION_ID": "child", "CODEX_THREAD_ID": "parent"}, err: ErrAmbiguous},
		{name: "stale same host identity", env: map[string]string{"CROSSAGENT_AGENT": "codex", "CROSSAGENT_SESSION_ID": "parent", "CODEX_THREAD_ID": "child"}, err: ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(Options{Getenv: func(k string) string { return tc.env[k] }})
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("got %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

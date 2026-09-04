// Package agent holds the agent-name vocabulary shared by every crossagent
// package. It is deliberately a leaf: it has no dependency on process
// execution, the filesystem, or any other crossagent package, so a caller can
// name an agent without importing the machinery that acts on one.
package agent

import (
	"errors"
	"fmt"
	"strings"
)

// Name is the canonical crossagent name of a coding-agent CLI. Its underlying
// type is string so a name can cross a JSON or CLI boundary unchanged; use
// Parse to turn untrusted input back into a Name.
type Name string

const (
	// Claude is Claude Code's canonical name.
	Claude Name = "claude"
	// Codex is Codex's canonical name.
	Codex Name = "codex"
	// Pi is pi's canonical name.
	Pi Name = "pi"
)

// ErrUnknown reports a value that is not one of the canonical names.
var ErrUnknown = errors.New("unknown agent")

// Names returns every canonical name in a stable order. The returned slice is
// independent and may be modified by the caller.
func Names() []Name {
	return []Name{Claude, Codex, Pi}
}

// Parse converts external input, such as a CLI flag or a stored record, into a
// Name. It trims surrounding whitespace and folds ASCII case, and it accepts
// one long-form alias per agent: "claude-code" for claude, "codex-cli" for
// codex, and "pi-coding-agent" for pi. Everything else, including the empty
// string, is an ErrUnknown error naming the rejected value.
func Parse(s string) (Name, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(Claude), "claude-code":
		return Claude, nil
	case string(Codex), "codex-cli":
		return Codex, nil
	case string(Pi), "pi-coding-agent":
		return Pi, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknown, s)
	}
}

// Valid reports whether n is one of the canonical names. A Name obtained from
// Parse is always valid; one built by converting a string may not be.
func (n Name) Valid() bool {
	switch n {
	case Claude, Codex, Pi:
		return true
	default:
		return false
	}
}

// String returns the canonical name as written on the wire.
func (n Name) String() string {
	return string(n)
}

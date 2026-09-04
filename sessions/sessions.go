// Package sessions discovers where Claude Code, Codex, and pi record their
// sessions. It reports location metadata only: a row is not proof that a
// process is running, and this package never reads /proc or probes liveness.
package sessions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ka2n/crossagent/agent"
	"github.com/ka2n/crossagent/paths"
)

// Session is a discovered agent session. Source identifies the CLI or storage
// record that produced the row. State is copied opaquely from a vendor source
// when one reports it; storage-derived rows leave it empty.
type Session struct {
	Agent        agent.Name `json:"agent"`
	SessionID    string     `json:"session_id"`
	Cwd          string     `json:"cwd"`
	Label        string     `json:"label,omitempty"`
	LastActivity time.Time  `json:"last_activity"`
	Source       string     `json:"source"`
	State        string     `json:"state"`
}

// CommandRunner runs an agent CLI. It is injectable so callers and tests can
// exercise a documented CLI interface without depending on a real
// installation.
type CommandRunner func(context.Context, string, ...string) ([]byte, error)

// SessionListerOptions configures the session locations and lister behavior.
// Empty agent-specific paths follow the paths package's environment variables
// and home-directory defaults. HomeDir and Resolver are injectable so tests do
// not need the machine's real home directory.
type SessionListerOptions struct {
	// Resolver supplies home and environment lookup behavior. HomeDir, when
	// non-empty, overrides Resolver.HomeDir for compatibility with the
	// simple fixture setup used by this package's original listers.
	Resolver paths.Resolver
	HomeDir  string

	// These explicit path fields are test/convenience overrides. They are
	// fed through Resolver's environment lookup rather than reconstructed
	// independently, so cwd encoding and relocation rules stay in paths.
	ClaudeConfigDir string
	CodexHome       string
	PiAgentDir      string
	PiSessionDir    string

	ClaudeCommand string
	ClaudeMaxAge  time.Duration
	// Limit caps one lister's returned result set after sorting. Zero means
	// no per-lister limit. List applies a limit to the combined result set.
	Limit int
	Now   func() time.Time

	CommandRunner CommandRunner
}

// SessionLister discovers sessions for one agent.
type SessionLister interface {
	List(context.Context) ([]Session, error)
}

// NewSessionListers returns the built-in listers keyed by their canonical
// agent name. Each lister is independent, so adding an agent does not change
// the storage logic of the others.
func NewSessionListers(opts SessionListerOptions) map[agent.Name]SessionLister {
	return map[agent.Name]SessionLister{
		agent.Claude: NewClaudeSessionLister(opts),
		agent.Codex:  NewCodexSessionLister(opts),
		agent.Pi:     NewPiSessionLister(opts),
	}
}

// List discovers sessions from all built-in agents in stable order. Limit is
// applied after the three listers are combined, matching a caller-facing
// result limit rather than limiting one agent preferentially.
func List(ctx context.Context, opts SessionListerOptions) ([]Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	perLister := opts
	perLister.Limit = 0
	listers := NewSessionListers(perLister)
	all := make([]Session, 0)
	for _, name := range agent.Names() {
		found, err := listers[name].List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s sessions: %w", name, err)
		}
		all = append(all, found...)
	}
	return finishResult(opts, all), nil
}

// ListAll is a descriptive alias for List.
func ListAll(ctx context.Context, opts SessionListerOptions) ([]Session, error) {
	return List(ctx, opts)
}

// Source values identify the backing source of a discovered row. A Claude row
// may contain two values joined with '+', because the CLI and transcript
// sources are unioned and merged by session id.
const (
	SourceClaudeAgents     = "claude-agents"
	SourceClaudeTranscript = "claude-transcript"
	SourceCodexRollout     = "codex-rollout"
	SourcePiSession        = "pi-session"
)

// DefaultClaudeMaxAge is the default mtime cutoff for Claude transcript scans.
const DefaultClaudeMaxAge = 7 * 24 * time.Hour

func (o SessionListerOptions) resolver() paths.Resolver {
	r := o.Resolver
	if strings.TrimSpace(o.HomeDir) != "" {
		r.HomeDir = o.HomeDir
	}
	previous := r.LookupEnv
	r.LookupEnv = func(key string) (string, bool) {
		switch key {
		case paths.ClaudeConfigDirEnv:
			if strings.TrimSpace(o.ClaudeConfigDir) != "" {
				return o.ClaudeConfigDir, true
			}
		case paths.CodexHomeEnv:
			if strings.TrimSpace(o.CodexHome) != "" {
				return o.CodexHome, true
			}
		case paths.PiAgentDirEnv:
			if strings.TrimSpace(o.PiAgentDir) != "" {
				return o.PiAgentDir, true
			}
		case paths.PiSessionDirEnv:
			if strings.TrimSpace(o.PiSessionDir) != "" {
				return o.PiSessionDir, true
			}
		}
		if previous != nil {
			return previous(key)
		}
		return os.LookupEnv(key)
	}
	return r
}

func (o SessionListerOptions) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	if o.CommandRunner != nil {
		return o.CommandRunner(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).Output()
}

func (o SessionListerOptions) claudeMaxAge() time.Duration {
	if o.ClaudeMaxAge > 0 {
		return o.ClaudeMaxAge
	}
	return DefaultClaudeMaxAge
}

func (o SessionListerOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func finishResult(opts SessionListerOptions, sessions []Session) []Session {
	seen := make(map[string]Session, len(sessions))
	for _, session := range sessions {
		if session.Agent == "" || strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.Cwd) == "" {
			continue
		}
		session.SessionID = strings.TrimSpace(session.SessionID)
		session.Cwd = filepath.Clean(strings.TrimSpace(session.Cwd))
		if session.Source == "" {
			session.Source = "unknown"
		}
		if session.Label == "" {
			session.Label = filepath.Base(session.Cwd)
		}
		key := session.Agent.String() + "\x00" + session.SessionID
		if previous, ok := seen[key]; !ok || session.LastActivity.After(previous.LastActivity) {
			seen[key] = session
		}
	}
	out := make([]Session, 0, len(seen))
	for _, session := range seen {
		out = append(out, session)
	}
	SortSessions(out)
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out
}

// SortSessions sorts sessions newest first, with deterministic tie breakers.
func SortSessions(sessions []Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		left, right := sessions[i], sessions[j]
		if !left.LastActivity.Equal(right.LastActivity) {
			return left.LastActivity.After(right.LastActivity)
		}
		if left.Agent != right.Agent {
			return left.Agent < right.Agent
		}
		if left.SessionID != right.SessionID {
			return left.SessionID < right.SessionID
		}
		return left.Cwd < right.Cwd
	})
}

// parseSessionTime accepts the timestamp forms used by the three agent
// stores: RFC3339 text, Unix seconds, and Unix milliseconds.
func parseSessionTime(raw json.RawMessage) time.Time {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return time.Time{}
	}
	if strings.HasPrefix(value, `"`) {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return time.Time{}
		}
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return parsed
		}
		value = text
	}
	if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
		if integer > 1e11 || integer < -1e11 {
			return time.UnixMilli(integer).UTC()
		}
		return time.Unix(integer, 0).UTC()
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return time.Time{}
	}
	if number > 1e11 || number < -1e11 {
		number /= 1000
	}
	seconds := int64(number)
	nanos := int64((number - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanos).UTC()
}

func parseTextTime(value string) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
		return parsed
	}
	return time.Time{}
}

func readFirstLine(path string, decode func([]byte) bool) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := newLineScanner(file)
	if !scanner.Scan() {
		return false
	}
	return decode(scanner.Bytes())
}

// newLineScanner keeps a large enough limit for Codex's metadata line, which
// includes serialized instructions, while still refusing unbounded input.
func newLineScanner(file *os.File) *bufio.Scanner {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return scanner
}

// newClaudeLineScanner handles unusually large Claude records without making
// the lister read an entire transcript into memory.
func newClaudeLineScanner(file *os.File) *bufio.Scanner {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	return scanner
}

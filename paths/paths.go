// Package paths resolves the documented and observed storage locations used by
// Claude Code, Codex, and pi. It only constructs paths; it never checks that a
// path exists or reads a configuration file.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/ka2n/crossagent/agent"
)

// Scope identifies a configuration scope shared by the agents where it
// applies. A scope that an agent does not support produces an empty path.
type Scope string

const (
	// ScopeUser is the per-user configuration scope.
	ScopeUser Scope = "user"
	// ScopeProject is configuration shared by a project checkout.
	ScopeProject Scope = "project"
	// ScopeLocal is per-user, unshared configuration inside a project.
	ScopeLocal Scope = "local"

	// User is a shorter alias for ScopeUser.
	User = ScopeUser
	// Project is a shorter alias for ScopeProject.
	Project = ScopeProject
	// Local is a shorter alias for ScopeLocal.
	Local = ScopeLocal
)

const (
	// ClaudeConfigDirEnv is the supported Claude Code configuration-directory
	// override. It relocates settings and session history from ~/.claude.
	ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"
	// CodexHomeEnv is the supported Codex per-user data-directory override.
	CodexHomeEnv = "CODEX_HOME"
	// PiAgentDirEnv is pi's supported configuration-directory override.
	PiAgentDirEnv = "PI_CODING_AGENT_DIR"
	// PiSessionDirEnv overrides pi's session directory independently of its
	// configuration directory.
	PiSessionDirEnv = "PI_CODING_AGENT_SESSION_DIR"
)

// LookupEnvFunc reads an environment variable. It is injectable so callers
// and tests can resolve paths without changing the process environment.
type LookupEnvFunc func(key string) (value string, ok bool)

// Resolver constructs agent paths relative to HomeDir and the supported
// environment overrides. HomeDir is injectable specifically so path tests do
// not need the machine's real home directory. An empty HomeDir reads HOME (or
// USERPROFILE) from the environment and performs no filesystem lookup.
type Resolver struct {
	HomeDir   string
	LookupEnv LookupEnvFunc
}

// NewResolver returns a Resolver using homeDir as the user's home directory.
// A blank homeDir means use os.UserHomeDir at resolution time.
func NewResolver(homeDir string) Resolver {
	return Resolver{HomeDir: homeDir}
}

// New is an alias for NewResolver.
func New(homeDir string) Resolver {
	return NewResolver(homeDir)
}

// DefaultResolver returns a Resolver that follows the current process home
// directory and environment. It does not inspect the filesystem.
func DefaultResolver() Resolver {
	return Resolver{}
}

// Home returns the home directory used by r. If r.HomeDir is empty, HOME or
// USERPROFILE is read from the environment. If neither is set it returns an
// empty string; callers that need an explicit home should inject it. No
// filesystem lookup is performed.
func (r Resolver) Home() string {
	if r.HomeDir != "" {
		return filepath.Clean(r.HomeDir)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if home, ok := r.lookupEnv(key); ok && strings.TrimSpace(home) != "" {
			return cleanConfiguredPath(home, "")
		}
	}
	return ""
}

func (r Resolver) lookupEnv(key string) (string, bool) {
	if r.LookupEnv != nil {
		return r.LookupEnv(key)
	}
	return os.LookupEnv(key)
}

func (r Resolver) configuredDir(envKey, fallback string) string {
	if value, ok := r.lookupEnv(envKey); ok && strings.TrimSpace(value) != "" {
		return cleanConfiguredPath(value, r.Home())
	}
	return filepath.Clean(fallback)
}

func cleanConfiguredPath(value, home string) string {
	value = strings.TrimSpace(value)
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		if home != "" {
			return filepath.Join(home, value[2:])
		}
	}
	return filepath.Clean(value)
}

func cleanCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Clean(cwd)
}

// ClaudeConfigDir returns Claude Code's user configuration directory. The
// default is $HOME/.claude; CLAUDE_CONFIG_DIR is a documented relocation and
// is preferred when non-empty.
func (r Resolver) ClaudeConfigDir() string {
	return r.configuredDir(ClaudeConfigDirEnv, filepath.Join(r.Home(), ".claude"))
}

// ClaudeConfigPath returns the Claude Code settings file for scope. User,
// project, and local paths are documented vendor settings files. Managed
// settings and one-session --settings inputs have no single portable path and
// therefore return an empty string when requested through an unsupported
// scope.
func (r Resolver) ClaudeConfigPath(scope Scope, cwd string) string {
	switch scope {
	case ScopeUser:
		return filepath.Join(r.ClaudeConfigDir(), "settings.json")
	case ScopeProject:
		return filepath.Join(cleanCWD(cwd), ".claude", "settings.json")
	case ScopeLocal:
		return filepath.Join(cleanCWD(cwd), ".claude", "settings.local.json")
	default:
		return ""
	}
}

// ClaudeSettingsPath is a descriptive alias for ClaudeConfigPath.
func (r Resolver) ClaudeSettingsPath(scope Scope, cwd string) string {
	return r.ClaudeConfigPath(scope, cwd)
}

// ClaudeSessionRoot returns the root containing Claude Code's per-cwd project
// directories. The layout is an observed local implementation detail, not an
// Anthropic path contract: <config-dir>/projects/.
func (r Resolver) ClaudeSessionRoot() string {
	return filepath.Join(r.ClaudeConfigDir(), "projects")
}

// ClaudeSessionDir returns the directory in which Claude Code records sessions
// for cwd. The cwd key is observed to replace every non-ASCII-alphanumeric
// character with '-'. This is intentionally one-way: lossy encoding must not
// be presented with a decoder.
func (r Resolver) ClaudeSessionDir(cwd string) string {
	return filepath.Join(r.ClaudeSessionRoot(), EncodeClaudeCWD(cwd))
}

// ClaudeSessionPath returns the observed Claude Code transcript path for a
// session id and working directory.
func (r Resolver) ClaudeSessionPath(cwd, sessionID string) string {
	return filepath.Join(r.ClaudeSessionDir(cwd), sessionID+".jsonl")
}

// ClaudeTranscriptPath is a descriptive alias for ClaudeSessionPath.
func (r Resolver) ClaudeTranscriptPath(cwd, sessionID string) string {
	return r.ClaudeSessionPath(cwd, sessionID)
}

// EncodeClaudeCWD returns Claude Code's observed project-directory key. Every
// character outside ASCII letters and digits becomes '-', including '/', '.',
// '_', spaces, and an existing '-'. The current implementation truncates
// keys over 200 UTF-16 code units and appends a base-36 hash. The transform is
// lossy, so no decoder is provided.
func EncodeClaudeCWD(cwd string) string {
	cwd = cleanCWD(cwd)
	units := utf16.Encode([]rune(cwd))
	var encoded strings.Builder
	encoded.Grow(len(units))
	for _, unit := range units {
		if unit <= 127 && isASCIIAlphaNumeric(byte(unit)) {
			encoded.WriteByte(byte(unit))
		} else {
			encoded.WriteByte('-')
		}
	}
	if encoded.Len() <= 200 {
		return encoded.String()
	}
	return encoded.String()[:200] + "-" + claudeCWDHash(units)
}

func claudeCWDHash(units []uint16) string {
	var hash int32
	for _, unit := range units {
		hash = (hash << 5) - hash + int32(unit)
	}
	value := int64(hash)
	if value < 0 {
		value = -value
	}
	return strconv.FormatInt(value, 36)
}

func isASCIIAlphaNumeric(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

// CodexHome returns Codex's per-user data directory. The default is
// $HOME/.codex; CODEX_HOME is a documented relocation. The helper does not
// validate the directory, matching this package's no-I/O contract.
func (r Resolver) CodexHome() string {
	return r.configuredDir(CodexHomeEnv, filepath.Join(r.Home(), ".codex"))
}

// CodexConfigPath returns Codex's config.toml path for the user or project
// scope. Codex has no distinct local settings file in the verified hook
// configuration layout.
func (r Resolver) CodexConfigPath(scope Scope, cwd string) string {
	switch scope {
	case ScopeUser:
		return filepath.Join(r.CodexHome(), "config.toml")
	case ScopeProject:
		return filepath.Join(cleanCWD(cwd), ".codex", "config.toml")
	default:
		return ""
	}
}

// CodexHooksPath returns Codex's hooks.json path for the user or project
// scope. The alternative [hooks] table in config.toml is available through
// CodexConfigPath.
func (r Resolver) CodexHooksPath(scope Scope, cwd string) string {
	switch scope {
	case ScopeUser:
		return filepath.Join(r.CodexHome(), "hooks.json")
	case ScopeProject:
		return filepath.Join(cleanCWD(cwd), ".codex", "hooks.json")
	default:
		return ""
	}
}

// CodexSessionRoot returns the root under which Codex writes dated rollout
// transcripts. The layout and filename are implementation-observed and can
// change independently of CODEX_HOME.
func (r Resolver) CodexSessionRoot() string {
	return filepath.Join(r.CodexHome(), "sessions")
}

// CodexSessionPath returns a glob pattern for a rollout with sessionID. Codex
// does not encode cwd in its path, and its date and start timestamp are not
// recoverable from (cwd, sessionID) alone; cwd is accepted for symmetry and
// deliberately has no effect. Use filepath.Glob or an index to resolve it.
func (r Resolver) CodexSessionPath(cwd, sessionID string) string {
	_ = cwd
	return r.CodexSessionPattern(sessionID)
}

// CodexSessionPattern returns the observed search pattern for a Codex rollout
// id: sessions/YYYY/MM/DD/rollout-<timestamp>-<id>.jsonl.
func (r Resolver) CodexSessionPattern(sessionID string) string {
	return filepath.Join(r.CodexSessionRoot(), "*", "*", "*", "rollout-*-"+sessionID+".jsonl")
}

// CodexRolloutPath returns an exact observed Codex rollout path when the
// session start time is known. Codex uses local time for both the dated
// directories and the colon-free filename timestamp; the cwd is not encoded.
func (r Resolver) CodexRolloutPath(sessionID string, startedAt time.Time) string {
	startedAt = startedAt.Local()
	date := filepath.Join(
		fmt.Sprintf("%04d", startedAt.Year()),
		fmt.Sprintf("%02d", int(startedAt.Month())),
		fmt.Sprintf("%02d", startedAt.Day()),
	)
	stamp := startedAt.Format("2006-01-02T15-04-05")
	return filepath.Join(r.CodexSessionRoot(), date, "rollout-"+stamp+"-"+sessionID+".jsonl")
}

// CodexTranscriptPath is an alias for CodexSessionPath and therefore returns
// a glob pattern unless an exact path is requested with CodexRolloutPath.
func (r Resolver) CodexTranscriptPath(cwd, sessionID string) string {
	return r.CodexSessionPath(cwd, sessionID)
}

// PiAgentDir returns pi's configuration directory. The default is
// $HOME/.pi/agent; PI_CODING_AGENT_DIR is documented as its relocation.
func (r Resolver) PiAgentDir() string {
	return r.configuredDir(PiAgentDirEnv, filepath.Join(r.Home(), ".pi", "agent"))
}

// PiConfigPath returns pi's settings.json for the user or project scope. pi
// has no declarative hook file; project settings are the observed .pi/settings.json
// surface used for project-local extension/package configuration.
func (r Resolver) PiConfigPath(scope Scope, cwd string) string {
	switch scope {
	case ScopeUser:
		return filepath.Join(r.PiAgentDir(), "settings.json")
	case ScopeProject:
		return filepath.Join(cleanCWD(cwd), ".pi", "settings.json")
	default:
		return ""
	}
}

// PiSettingsPath is a descriptive alias for PiConfigPath.
func (r Resolver) PiSettingsPath(scope Scope, cwd string) string {
	return r.PiConfigPath(scope, cwd)
}

// PiSessionRoot returns pi's session storage root. PI_CODING_AGENT_SESSION_DIR
// is the documented override for this root; otherwise the default is
// <agent-dir>/sessions.
func (r Resolver) PiSessionRoot() string {
	if value, ok := r.lookupEnv(PiSessionDirEnv); ok && strings.TrimSpace(value) != "" {
		return cleanConfiguredPath(value, r.Home())
	}
	return filepath.Join(r.PiAgentDir(), "sessions")
}

// PiSessionDir returns the directory for cwd. With no explicit session-dir
// override, pi uses the upstream getDefaultSessionDirPath encoding. An
// explicit PI_CODING_AGENT_SESSION_DIR is already the complete directory and
// therefore does not append a cwd key.
func (r Resolver) PiSessionDir(cwd string) string {
	if value, ok := r.lookupEnv(PiSessionDirEnv); ok && strings.TrimSpace(value) != "" {
		return r.PiSessionRoot()
	}
	return filepath.Join(r.PiSessionRoot(), EncodePiCWD(cwd))
}

// PiSessionPath returns a glob pattern for a pi session id in cwd. pi includes
// a timestamp in every filename, so (cwd, sessionID) identifies a directory
// and filename pattern rather than one exact file.
func (r Resolver) PiSessionPath(cwd, sessionID string) string {
	return filepath.Join(r.PiSessionDir(cwd), "*_"+sessionID+".jsonl")
}

// PiSessionPattern is an alias for PiSessionPath.
func (r Resolver) PiSessionPattern(cwd, sessionID string) string {
	return r.PiSessionPath(cwd, sessionID)
}

// PiSessionFilePath returns an exact pi session filename when its creation
// timestamp is known. Pi serializes that timestamp as UTC ISO-8601 with
// colons and dots replaced by '-'.
func (r Resolver) PiSessionFilePath(cwd, sessionID string, startedAt time.Time) string {
	stamp := startedAt.UTC().Format("2006-01-02T15-04-05.000Z")
	stamp = strings.NewReplacer(":", "-", ".", "-").Replace(stamp)
	return filepath.Join(r.PiSessionDir(cwd), stamp+"_"+sessionID+".jsonl")
}

// PiTranscriptPath is a descriptive alias for PiSessionPath.
func (r Resolver) PiTranscriptPath(cwd, sessionID string) string {
	return r.PiSessionPath(cwd, sessionID)
}

// EncodePiCWD returns the key used by pi's upstream getDefaultSessionDirPath.
// The resolved path loses one leading separator, every '/', '\\', or drive
// ':' becomes '-', and the result is wrapped in literal '--' delimiters.
// Dots and underscores are retained. The transform is lossy, so no decoder is
// provided.
func EncodePiCWD(cwd string) string {
	cwd = cleanCWD(cwd)
	if len(cwd) > 0 && (cwd[0] == '/' || cwd[0] == '\\') {
		cwd = cwd[1:]
	}
	var encoded strings.Builder
	encoded.Grow(len(cwd) + 4)
	for i := 0; i < len(cwd); i++ {
		switch cwd[i] {
		case '/', '\\', ':':
			encoded.WriteByte('-')
		default:
			encoded.WriteByte(cwd[i])
		}
	}
	return "--" + encoded.String() + "--"
}

// SessionRoot returns the session root for a known agent name. It is a
// convenience for code that dispatches on detection results. An invalid name
// returns an empty string.
func (r Resolver) SessionRoot(name agent.Name) string {
	switch name {
	case agent.Claude:
		return r.ClaudeSessionRoot()
	case agent.Codex:
		return r.CodexSessionRoot()
	case agent.Pi:
		return r.PiSessionRoot()
	default:
		return ""
	}
}

// ConfigPath returns an agent-specific user/project/local configuration path.
// Unsupported scopes and invalid names return an empty string.
func (r Resolver) ConfigPath(name agent.Name, scope Scope, cwd string) string {
	switch name {
	case agent.Claude:
		return r.ClaudeConfigPath(scope, cwd)
	case agent.Codex:
		return r.CodexConfigPath(scope, cwd)
	case agent.Pi:
		return r.PiConfigPath(scope, cwd)
	default:
		return ""
	}
}

// SessionDir returns an agent-specific session directory for cwd. Codex does
// not encode cwd, so it returns its session root; an explicit pi session-dir
// override likewise returns that complete directory.
func (r Resolver) SessionDir(name agent.Name, cwd string) string {
	switch name {
	case agent.Claude:
		return r.ClaudeSessionDir(cwd)
	case agent.Codex:
		return r.CodexSessionRoot()
	case agent.Pi:
		return r.PiSessionDir(cwd)
	default:
		return ""
	}
}

// SessionPath returns an agent-specific transcript path. Claude returns an
// exact path; Codex and pi return patterns because their filenames contain
// timestamps that are not derivable from an id and cwd alone.
func (r Resolver) SessionPath(name agent.Name, cwd, sessionID string) string {
	switch name {
	case agent.Claude:
		return r.ClaudeSessionPath(cwd, sessionID)
	case agent.Codex:
		return r.CodexSessionPath(cwd, sessionID)
	case agent.Pi:
		return r.PiSessionPath(cwd, sessionID)
	default:
		return ""
	}
}

package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ka2n/crossagent/agent"
)

func noEnvironment(string) (string, bool) { return "", false }

func TestEncodeCWDs(t *testing.T) {
	tests := []struct {
		name   string
		cwd    string
		claude string
		pi     string
	}{
		{
			name:   "verified repository",
			cwd:    "/home/katsuma/src/github.com/ka2n/jill",
			claude: "-home-katsuma-src-github-com-ka2n-jill",
			pi:     "--home-katsuma-src-github.com-ka2n-jill--",
		},
		{
			name:   "punctuation underscore and space",
			cwd:    "/tmp/project.with_under score/",
			claude: "-tmp-project-with-under-score",
			pi:     "--tmp-project.with_under score--",
		},
		{
			name:   "dot directory and underscore",
			cwd:    "/home/u/.config/project_name/",
			claude: "-home-u--config-project-name",
			pi:     "--home-u-.config-project_name--",
		},
		{
			name:   "root",
			cwd:    "/",
			claude: "-",
			pi:     "----",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeClaudeCWD(tt.cwd); got != tt.claude {
				t.Fatalf("EncodeClaudeCWD(%q) = %q, want %q", tt.cwd, got, tt.claude)
			}
			if got := EncodePiCWD(tt.cwd); got != tt.pi {
				t.Fatalf("EncodePiCWD(%q) = %q, want %q", tt.cwd, got, tt.pi)
			}
		})
	}
}

func TestEncodeClaudeCWDLongPathUsesObservedHashSuffix(t *testing.T) {
	cwd := "/" + strings.Repeat("a", 205)
	want := "-" + strings.Repeat("a", 199) + "-bn8w8e"
	if got := EncodeClaudeCWD(cwd); got != want {
		t.Fatalf("EncodeClaudeCWD(long path) = %q, want %q", got, want)
	}
}

func TestVerifiedEncodingDirectoriesExist(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(home)
	r.LookupEnv = noEnvironment
	cwd := "/home/katsuma/src/github.com/ka2n/jill"

	claudeDir := r.ClaudeSessionDir(cwd)
	if info, err := os.Stat(claudeDir); err != nil || !info.IsDir() {
		t.Fatalf("verified Claude session directory %q is not present: %v", claudeDir, err)
	}
	piDir := r.PiSessionDir(cwd)
	if info, err := os.Stat(piDir); err != nil || !info.IsDir() {
		t.Fatalf("verified pi session directory %q is not present: %v", piDir, err)
	}
}

func TestResolverHonorsEnvironmentOverrides(t *testing.T) {
	env := map[string]string{
		ClaudeConfigDirEnv: "/override/claude",
		CodexHomeEnv:       "/override/codex",
		PiAgentDirEnv:      "/override/pi/agent",
		PiSessionDirEnv:    "/override/pi/sessions",
	}
	r := NewResolver("/injected/home")
	r.LookupEnv = func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	}

	if got := r.ClaudeConfigDir(); got != "/override/claude" {
		t.Errorf("ClaudeConfigDir() = %q", got)
	}
	if got := r.CodexHome(); got != "/override/codex" {
		t.Errorf("CodexHome() = %q", got)
	}
	if got := r.PiAgentDir(); got != "/override/pi/agent" {
		t.Errorf("PiAgentDir() = %q", got)
	}
	if got := r.PiSessionRoot(); got != "/override/pi/sessions" {
		t.Errorf("PiSessionRoot() = %q", got)
	}
	if got := r.PiSessionDir("/work/project"); got != "/override/pi/sessions" {
		t.Errorf("PiSessionDir() with explicit root = %q", got)
	}
}

func TestResolverDefaultDirectoriesAndConfigScopes(t *testing.T) {
	r := NewResolver("/home/tester")
	r.LookupEnv = noEnvironment
	cwd := "/work/project"

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Claude user", r.ClaudeConfigPath(ScopeUser, cwd), "/home/tester/.claude/settings.json"},
		{"Claude project", r.ClaudeConfigPath(ScopeProject, cwd), "/work/project/.claude/settings.json"},
		{"Claude local", r.ClaudeConfigPath(ScopeLocal, cwd), "/work/project/.claude/settings.local.json"},
		{"Codex user", r.CodexConfigPath(ScopeUser, cwd), "/home/tester/.codex/config.toml"},
		{"Codex project", r.CodexConfigPath(ScopeProject, cwd), "/work/project/.codex/config.toml"},
		{"Codex user hooks", r.CodexHooksPath(ScopeUser, cwd), "/home/tester/.codex/hooks.json"},
		{"Codex project hooks", r.CodexHooksPath(ScopeProject, cwd), "/work/project/.codex/hooks.json"},
		{"pi user", r.PiConfigPath(ScopeUser, cwd), "/home/tester/.pi/agent/settings.json"},
		{"pi project", r.PiConfigPath(ScopeProject, cwd), "/work/project/.pi/settings.json"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if got := r.CodexConfigPath(ScopeLocal, cwd); got != "" {
		t.Errorf("Codex local config = %q, want unsupported empty path", got)
	}
	if got := r.PiConfigPath(ScopeLocal, cwd); got != "" {
		t.Errorf("pi local config = %q, want unsupported empty path", got)
	}
}

func TestSessionPaths(t *testing.T) {
	r := NewResolver("/home/tester")
	r.LookupEnv = noEnvironment
	cwd := "/work/project"

	if got, want := r.ClaudeSessionRoot(), "/home/tester/.claude/projects"; got != want {
		t.Errorf("ClaudeSessionRoot() = %q, want %q", got, want)
	}
	if got, want := r.ClaudeSessionPath(cwd, "session-1"), "/home/tester/.claude/projects/-work-project/session-1.jsonl"; got != want {
		t.Errorf("ClaudeSessionPath() = %q, want %q", got, want)
	}
	if got, want := r.CodexSessionRoot(), "/home/tester/.codex/sessions"; got != want {
		t.Errorf("CodexSessionRoot() = %q, want %q", got, want)
	}
	if got, want := r.CodexSessionPath(cwd, "thread-1"), "/home/tester/.codex/sessions/*/*/*/rollout-*-thread-1.jsonl"; got != want {
		t.Errorf("CodexSessionPath() = %q, want %q", got, want)
	}
	if got, want := r.PiSessionRoot(), "/home/tester/.pi/agent/sessions"; got != want {
		t.Errorf("PiSessionRoot() = %q, want %q", got, want)
	}
	if got, want := r.PiSessionPath(cwd, "session-1"), "/home/tester/.pi/agent/sessions/--work-project--/*_session-1.jsonl"; got != want {
		t.Errorf("PiSessionPath() = %q, want %q", got, want)
	}

	started := time.Date(2026, 9, 4, 12, 34, 56, 789000000, time.UTC)
	codexStamp := started.Local().Format("2006-01-02T15-04-05")
	codexWant := filepath.Join("/home/tester/.codex/sessions", "2026", "09", "04", "rollout-"+codexStamp+"-thread-1.jsonl")
	if got := r.CodexRolloutPath("thread-1", started); got != codexWant {
		t.Errorf("CodexRolloutPath() = %q, want %q", got, codexWant)
	}
	piStamp := strings.NewReplacer(":", "-", ".", "-").Replace(started.UTC().Format("2006-01-02T15-04-05.000Z"))
	piWant := filepath.Join("/home/tester/.pi/agent/sessions", "--work-project--", piStamp+"_session-1.jsonl")
	if got := r.PiSessionFilePath(cwd, "session-1", started); got != piWant {
		t.Errorf("PiSessionFilePath() = %q, want %q", got, piWant)
	}
}

func TestTildeEnvironmentOverrideUsesInjectedHome(t *testing.T) {
	r := NewResolver("/injected/home")
	r.LookupEnv = func(key string) (string, bool) {
		if key == PiAgentDirEnv {
			return "~/custom/pi", true
		}
		return "", false
	}
	if got, want := r.PiAgentDir(), filepath.Join("/injected/home", "custom/pi"); got != want {
		t.Errorf("PiAgentDir() = %q, want %q", got, want)
	}
}

func TestAgentDispatchersFollowTheTypedName(t *testing.T) {
	r := Resolver{HomeDir: "/home/tester", LookupEnv: noEnvironment}
	const cwd = "/work/project"
	for _, tc := range []struct {
		name        agent.Name
		sessionRoot string
		configPath  string
		sessionDir  string
		sessionPath string
	}{
		{
			name:        agent.Claude,
			sessionRoot: "/home/tester/.claude/projects",
			configPath:  "/home/tester/.claude/settings.json",
			sessionDir:  "/home/tester/.claude/projects/-work-project",
			sessionPath: "/home/tester/.claude/projects/-work-project/session-1.jsonl",
		},
		{
			name:        agent.Codex,
			sessionRoot: "/home/tester/.codex/sessions",
			configPath:  "/home/tester/.codex/config.toml",
			sessionDir:  "/home/tester/.codex/sessions",
			sessionPath: "/home/tester/.codex/sessions/*/*/*/rollout-*-session-1.jsonl",
		},
		{
			name:        agent.Pi,
			sessionRoot: "/home/tester/.pi/agent/sessions",
			configPath:  "/home/tester/.pi/agent/settings.json",
			sessionDir:  "/home/tester/.pi/agent/sessions/--work-project--",
			sessionPath: "/home/tester/.pi/agent/sessions/--work-project--/*_session-1.jsonl",
		},
	} {
		t.Run(tc.name.String(), func(t *testing.T) {
			if got := r.SessionRoot(tc.name); got != tc.sessionRoot {
				t.Errorf("SessionRoot() = %q, want %q", got, tc.sessionRoot)
			}
			if got := r.ConfigPath(tc.name, ScopeUser, cwd); got != tc.configPath {
				t.Errorf("ConfigPath() = %q, want %q", got, tc.configPath)
			}
			if got := r.SessionDir(tc.name, cwd); got != tc.sessionDir {
				t.Errorf("SessionDir() = %q, want %q", got, tc.sessionDir)
			}
			if got := r.SessionPath(tc.name, cwd, "session-1"); got != tc.sessionPath {
				t.Errorf("SessionPath() = %q, want %q", got, tc.sessionPath)
			}
		})
	}
}

func TestAgentDispatchersRejectInvalidNames(t *testing.T) {
	r := Resolver{HomeDir: "/home/tester", LookupEnv: noEnvironment}
	// Alias spellings are agent.Parse's job, so the dispatchers see only
	// canonical names and treat anything else as unknown.
	for _, name := range []agent.Name{"", "gemini", "claude-code", "Claude"} {
		if got := r.SessionRoot(name); got != "" {
			t.Errorf("SessionRoot(%q) = %q, want empty", name, got)
		}
		if got := r.ConfigPath(name, ScopeUser, "/work/project"); got != "" {
			t.Errorf("ConfigPath(%q) = %q, want empty", name, got)
		}
		if got := r.SessionDir(name, "/work/project"); got != "" {
			t.Errorf("SessionDir(%q) = %q, want empty", name, got)
		}
		if got := r.SessionPath(name, "/work/project", "session-1"); got != "" {
			t.Errorf("SessionPath(%q) = %q, want empty", name, got)
		}
	}
}

package crossagent

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestDetectorFindsVersionsAndCapabilities(t *testing.T) {
	outputs := map[string]string{
		"claude": "2.1.251 (Claude Code)\n",
		"codex":  "codex-cli 0.151.0\n",
		"pi":     "0.84.4\n",
	}
	var calls []string
	detector := Detector{
		LookPath: func(name string) (string, error) {
			return "/fake/bin/" + name, nil
		},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if len(args) != 1 || args[0] != "--version" {
				t.Fatalf("runner args = %v, want --version", args)
			}
			calls = append(calls, name)
			return []byte(outputs[filepath.Base(name)]), nil
		},
	}

	agents, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("Detect() returned %d agents, want 3", len(agents))
	}
	want := []Agent{
		{Name: Claude, Binary: "claude", Path: "/fake/bin/claude", Found: true, Version: "2.1.251", Capabilities: CapabilityHooks},
		{
			Name: Codex, Binary: "codex", Path: "/fake/bin/codex", Found: true, Version: "0.151.0",
			Capabilities: CapabilityHooks | CapabilityExternalMessageQueue,
		},
		{Name: Pi, Binary: "pi", Path: "/fake/bin/pi", Found: true, Version: "0.84.4", Capabilities: CapabilityExtensions | CapabilityRPCMode},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Fatalf("Detect() = %+v, want %+v", agents, want)
	}
	if !reflect.DeepEqual(calls, []string{"/fake/bin/claude", "/fake/bin/codex", "/fake/bin/pi"}) {
		t.Fatalf("runner calls = %v", calls)
	}
}

func TestDetectorReportsMissingAgentWithoutError(t *testing.T) {
	detector := Detector{
		LookPath: func(name string) (string, error) {
			if name == "codex" {
				return "", exec.ErrNotFound
			}
			return "/fake/" + name, nil
		},
		Run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			return []byte("1.0.0"), nil
		},
	}

	detected, err := detector.DetectOne(context.Background(), Codex)
	if err != nil {
		t.Fatal(err)
	}
	if detected.Found || detected.Path != "" || detected.Version != "" {
		t.Fatalf("missing agent = %+v", detected)
	}
	if !detected.Capabilities.Has(CapabilityExternalMessageQueue) {
		t.Fatal("missing Codex result lost its static capability description")
	}
}

func TestDetectorVersionErrorStillReportsFoundAgent(t *testing.T) {
	detector := Detector{
		LookPath: func(string) (string, error) { return "/fake/claude", nil },
		Run:      func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("failed") },
	}

	detected, err := detector.DetectOne(context.Background(), Claude)
	if err == nil {
		t.Fatal("DetectOne() returned nil error for failed version command")
	}
	if !detected.Found || detected.Path != "/fake/claude" {
		t.Fatalf("version failure result = %+v", detected)
	}
}

func TestDetectOneUnknownAgent(t *testing.T) {
	// DetectOne takes a canonical Name and does not normalize: an alias
	// spelling belongs to agent.Parse, which is where CLI input is converted.
	for _, name := range []Name{"gemini", "claude-code", "", " claude "} {
		if _, err := (Detector{}).DetectOne(context.Background(), name); !errors.Is(err, ErrUnknownAgent) {
			t.Fatalf("DetectOne(%q) error = %v, want ErrUnknownAgent", name, err)
		}
	}
}

// TestNameConstantsAliasTheAgentPackage pins the re-export: the root's Name
// and constants are the agent package's, so a value built with one is usable
// with the other and ErrUnknownAgent matches a parse rejection.
func TestNameConstantsAliasTheAgentPackage(t *testing.T) {
	if Claude != agent.Claude || Codex != agent.Codex || Pi != agent.Pi {
		t.Fatal("root constants are not the agent package's constants")
	}
	var name Name = agent.Codex
	if !name.Valid() || name.String() != "codex" {
		t.Fatalf("Name alias lost the agent.Name methods: %q", name)
	}
	if _, err := agent.Parse("gemini"); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("agent.Parse error = %v, want ErrUnknownAgent", err)
	}
}

func TestParseVersionIsLenient(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"2.1.251 (Claude Code)", "2.1.251"},
		{"codex-cli 0.151.0", "0.151.0"},
		{"version: v0.84.4", "0.84.4"},
		{"version unavailable", ""},
	}
	for _, tt := range tests {
		if got := ParseVersion([]byte(tt.output)); got != tt.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestCapabilities(t *testing.T) {
	caps := CapabilityHooks | CapabilityRPCMode
	if !caps.Has(CapabilityHooks) || !caps.Contains(CapabilityRPCMode) {
		t.Fatal("Has/Contains failed")
	}
	if caps.Has(CapabilityExtensions) || caps.Has(0) {
		t.Fatal("unexpected capability")
	}
	if got := caps.String(); got != "hooks,rpc_mode" {
		t.Fatalf("Capabilities.String() = %q", got)
	}
}

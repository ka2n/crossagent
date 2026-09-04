package crossagent

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
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
		{Name: "claude", Binary: "claude", Path: "/fake/bin/claude", Found: true, Version: "2.1.251", Capabilities: CapabilityHooks},
		{
			Name: "codex", Binary: "codex", Path: "/fake/bin/codex", Found: true, Version: "0.151.0",
			Capabilities: CapabilityHooks | CapabilityExternalMessageQueue,
			ExternalMessageCommand: &ExternalMessageCommand{
				Binary: "codex",
				Args:   []string{"queue", "--thread", PlaceholderThreadID, "--message", PlaceholderMessage},
			},
		},
		{Name: "pi", Binary: "pi", Path: "/fake/bin/pi", Found: true, Version: "0.84.4", Capabilities: CapabilityExtensions | CapabilityRPCMode},
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

	agent, err := detector.DetectOne(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if agent.Found || agent.Path != "" || agent.Version != "" {
		t.Fatalf("missing agent = %+v", agent)
	}
	if !agent.Capabilities.Has(CapabilityExternalMessageQueue) || agent.ExternalMessageCommand == nil {
		t.Fatal("missing Codex result lost its static capability description")
	}
}

func TestExternalMessageCommandOnlyBuildsArgv(t *testing.T) {
	command := ExternalMessageCommand{
		Binary: "codex",
		Args:   []string{"queue", "--thread", PlaceholderThreadID, "--message", PlaceholderMessage},
	}
	want := []string{"codex", "queue", "--thread", "thread-1", "--message", "hello world"}
	if got := command.Command("thread-1", "hello world"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Command() = %v, want %v", got, want)
	}
	if got := command.Arguments("thread-1", "hello world"); !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("Arguments() = %v, want %v", got, want[1:])
	}
}

func TestDetectorVersionErrorStillReportsFoundAgent(t *testing.T) {
	detector := Detector{
		LookPath: func(string) (string, error) { return "/fake/claude", nil },
		Run:      func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("failed") },
	}

	agent, err := detector.DetectOne(context.Background(), "claude")
	if err == nil {
		t.Fatal("DetectOne() returned nil error for failed version command")
	}
	if !agent.Found || agent.Path != "/fake/claude" {
		t.Fatalf("version failure result = %+v", agent)
	}
}

func TestDetectOneUnknownAgent(t *testing.T) {
	_, err := (Detector{}).DetectOne(context.Background(), "gemini")
	if !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("DetectOne() error = %v, want ErrUnknownAgent", err)
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

package hooks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchBasenameTable(t *testing.T) {
	matcher := MatchBasename("mytool")
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "absolute path", command: "/opt/bin/mytool collect", want: true},
		{name: "quoted path", command: `"/opt/bin/mytool" collect`, want: true},
		{name: "suffix is ignored", command: BuildCommandSuffixMarker("/opt/bin/mytool collect", "mytool", "collect"), want: true},
		{name: "assignment is not unwrapped", command: "DEBUG=1 /opt/bin/mytool collect"},
		{name: "env is not unwrapped", command: "env DEBUG=1 /opt/bin/mytool collect"},
		{name: "similar basename", command: "/opt/bin/mytool-extra collect"},
		{name: "shell operator stays part of first word", command: "/opt/bin/mytool; other-tool collect"},
		{name: "empty command", command: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matcher.Match(HookEntry{Command: tt.command}); got != tt.want {
				t.Fatalf("Match(%q) = %t, want %t", tt.command, got, tt.want)
			}
		})
	}
}

func TestMatchPatternTableAndCompilation(t *testing.T) {
	anchored, err := MatchPattern(`^/opt/bin/mytool(?:\s|$)`)
	if err != nil {
		t.Fatal(err)
	}
	unanchored, err := MatchPattern(`mytool`)
	if err != nil {
		t.Fatal(err)
	}
	suffixCommand := BuildCommandSuffixMarker("/opt/bin/mytool collect", "mytool", "collect")
	tests := []struct {
		name    string
		matcher Matcher
		command string
		want    bool
	}{
		{name: "anchored command", matcher: anchored, command: "/opt/bin/mytool collect", want: true},
		{name: "anchored strips suffix", matcher: anchored, command: suffixCommand, want: true},
		{name: "anchored rejects other executable", matcher: anchored, command: "/opt/bin/other mytool collect"},
		{name: "unanchored sees later text", matcher: unanchored, command: "/opt/bin/other mytool collect", want: true},
		{name: "pattern does not unwrap env", matcher: anchored, command: "env DEBUG=1 /opt/bin/mytool collect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.matcher.Match(HookEntry{Command: tt.command}); got != tt.want {
				t.Fatalf("Match(%q) = %t, want %t", tt.command, got, tt.want)
			}
		})
	}

	invalid, err := MatchPattern("[")
	if err == nil {
		t.Fatal("invalid pattern compiled without an error")
	}
	if invalid == nil {
		t.Fatal("invalid pattern did not return a plan-invalid matcher")
	}
	if invalid.Match(HookEntry{Command: "anything"}) {
		t.Fatal("invalid pattern matcher matched a command")
	}
}

func TestMatchEnvWrappedPreservesUnwrapVectors(t *testing.T) {
	matcher := MatchEnvWrapped(MatchBasename("mytool"))
	for _, command := range []string{
		"DEBUG=1 /usr/local/bin/mytool hook",
		"env DEBUG=1 --unset OLD /usr/local/bin/mytool hook --flag",
		`env 'DEBUG=1' "/usr/local/bin/mytool" hook`,
		BuildCommandSuffixMarker("env DEBUG=1 /usr/local/bin/mytool hook", "mytool", "hook"),
		"/usr/local/bin/mytool hook",
	} {
		if !matcher.Match(HookEntry{Command: command}) {
			t.Errorf("wrapped matcher did not recognize %q", command)
		}
	}
	for _, command := range []string{
		"env DEBUG=1 other-tool hook",
		"/usr/local/bin/mytool-other hook",
		"/usr/local/bin/mytool; other-tool hook",
		"env --unset OLD",
	} {
		if matcher.Match(HookEntry{Command: command}) {
			t.Errorf("wrapped matcher falsely recognized %q", command)
		}
	}
}

func TestMatchEnvWrappedDelegatesCleanCommand(t *testing.T) {
	matcher, err := MatchPattern(`^/usr/local/bin/mytool(?:\s|$)`)
	if err != nil {
		t.Fatal(err)
	}
	matcher = MatchEnvWrapped(matcher)
	command := BuildCommandSuffixMarker("env DEBUG=1 /usr/local/bin/mytool hook", "mytool", "hook")
	if !matcher.Match(HookEntry{Command: command}) {
		t.Fatalf("wrapped pattern did not match %q", command)
	}
}

func TestMatchAnyAndAll(t *testing.T) {
	any := MatchAny(MatchBasename("oldtool"), MatchBasename("newtool"))
	all := MatchAll(MatchBasename("mytool"), mustMatchPattern(t, `^/opt/mytool collect$`))
	suffix := BuildCommandSuffixMarker("/opt/mytool collect", "mytool", "collect")
	tests := []struct {
		name    string
		matcher Matcher
		command string
		want    bool
	}{
		{name: "any old name", matcher: any, command: "/opt/oldtool collect", want: true},
		{name: "any new name", matcher: any, command: "/opt/newtool collect", want: true},
		{name: "any rejects foreign", matcher: any, command: "/opt/other collect"},
		{name: "all succeeds", matcher: all, command: "/opt/mytool collect", want: true},
		{name: "all strips suffix", matcher: all, command: suffix, want: true},
		{name: "all rejects wrong action", matcher: all, command: "/opt/mytool stop"},
		{name: "all rejects wrong basename", matcher: all, command: "/opt/other collect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.matcher.Match(HookEntry{Command: tt.command}); got != tt.want {
				t.Fatalf("Match(%q) = %t, want %t", tt.command, got, tt.want)
			}
		})
	}
	if MatchAny().Match(HookEntry{Command: "anything"}) {
		t.Fatal("empty MatchAny matched")
	}
	if !MatchAll().Match(HookEntry{Command: "anything"}) {
		t.Fatal("empty MatchAll did not have the logical identity result")
	}
}

func TestMatchFuncGetsCleanCommandAndCompleteCopiedFields(t *testing.T) {
	command := BuildCommandSuffixMarker("/opt/mytool collect", "mytool", "collect")
	fields := map[string]any{
		"type":    "command",
		"command": command,
		"vendor":  map[string]any{"enabled": true},
	}
	matcher := MatchFunc(func(entry HookEntry) bool {
		if entry.Command != "/opt/mytool collect" {
			t.Errorf("MatchFunc command = %q", entry.Command)
		}
		if entry.Fields["command"] != command || entry.Fields["vendor"].(map[string]any)["enabled"] != true {
			t.Errorf("MatchFunc fields = %#v", entry.Fields)
		}
		entry.Fields["mutated"] = true
		return true
	})
	if !matcher.Match(HookEntry{Command: command, Fields: fields}) {
		t.Fatal("MatchFunc returned false")
	}
	if _, ok := fields["mutated"]; ok {
		t.Fatalf("MatchFunc mutated caller fields: %#v", fields)
	}
}

func TestInvalidMatchersFailAtPlanTime(t *testing.T) {
	invalid, patternErr := MatchPattern("[")
	if patternErr == nil {
		t.Fatal("invalid pattern did not return an error")
	}
	manager := configManager(filepath.Join(t.TempDir(), "settings.json"), "mytool", HookSpec{
		Event: EventStop, Command: "mytool stop", ID: "stop",
	})
	manager.Matcher = invalid
	if _, err := manager.PlanInstall(); err == nil || !strings.Contains(err.Error(), "invalid hook matcher") {
		t.Fatalf("ignored pattern error produced plan error %v", err)
	}

	manager.Matcher = MatchAny(nil)
	if _, err := manager.PlanInstall(); err == nil || !strings.Contains(err.Error(), "matcher 0") {
		t.Fatalf("nil composite matcher produced plan error %v", err)
	}

	manager.Matcher = MatchFunc(nil)
	if _, err := manager.PlanInstall(); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil function matcher produced plan error %v", err)
	}
}

func TestDefaultMatcherLeavesEnvWrappedForeignEntryUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	wrapped := "env DEBUG=1 /old/mytool collect"
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": wrapped},
		}}}},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool collect", ID: "collect"})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unmarked) != 0 || len(plan.Added) != 1 || len(plan.Removed) != 0 {
		t.Fatalf("default env-wrapped plan = %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 2 {
		t.Fatalf("default matcher entries = %#v", entries)
	}
	foundWrapped := false
	for _, entry := range entries {
		if entryCommand(entry) == wrapped {
			foundWrapped = true
		}
	}
	if !foundWrapped {
		t.Fatalf("default matcher changed env-wrapped foreign entry: %#v", entries)
	}
}

func TestEnvWrappedAdoptionReplacesWrapperWithDeclaredCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	wrapped := "env DEBUG=1 /old/mytool collect"
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": wrapped},
		}}}},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool collect", ID: "collect"})
	manager.MarkerStyle = MarkerStyleCommandSuffix
	manager.Matcher = MatchEnvWrapped(MatchBasename("mytool"))
	manager.AdoptUnmarked = true
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	wantCommand := BuildCommandSuffixMarker("/new/mytool collect", "mytool", "collect")
	if len(plan.Unmarked) != 1 || len(plan.Added) != 1 || len(plan.Removed) != 1 {
		t.Fatalf("env-wrapped adoption plan = %+v", plan)
	}
	if !strings.Contains(plan.Diff, "env DEBUG=1 /old/mytool collect") || !strings.Contains(plan.Diff, wantCommand) {
		t.Fatalf("adoption diff does not show wrapper replacement: %s", plan.Diff)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 || entryCommand(entries[0]) != wantCommand {
		t.Fatalf("env-wrapped adoption entries = %#v", entries)
	}
}

func TestMatchAnyBridgesBinaryRenameDuringConvergence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "/old/oldtool collect --hand-edited"},
		}}}},
	}, 0o644)
	manager := configManager(path, "/new/newtool", HookSpec{Event: EventStop, Command: "/new/newtool collect", ID: "collect"})
	manager.ToolName = "newtool"
	manager.Matcher = MatchAny(MatchBasename("oldtool"), MatchBasename("newtool"))
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges || plan.Summary.Modified != 1 || len(plan.Added) != 1 || len(plan.Removed) != 1 {
		t.Fatalf("rename convergence plan = %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 || entryCommand(entries[0]) != "/new/newtool collect" {
		t.Fatalf("rename convergence entries = %#v", entries)
	}
}

func mustMatchPattern(t *testing.T, expression string) Matcher {
	t.Helper()
	matcher, err := MatchPattern(expression)
	if err != nil {
		t.Fatal(err)
	}
	return matcher
}

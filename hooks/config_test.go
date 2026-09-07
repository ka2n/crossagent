package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func configManager(path, invocation string, specs ...HookSpec) ConfigManager {
	return ConfigManager{
		Agent:        AgentClaude,
		SettingsPath: path,
		ToolName:     "mytool",
		Invocation:   invocation,
		MarkerStyle:  MarkerStyleNone,
		Hooks:        specs,
	}
}

func writeConfig(t *testing.T, path string, settings map[string]any, mode os.FileMode) {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

func readConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func commandEntries(t *testing.T, settings map[string]any, event string) []map[string]any {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	groups, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	var entries []map[string]any
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := group["hooks"].([]any)
		for _, rawEntry := range inner {
			entry, ok := rawEntry.(map[string]any)
			if ok {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

func entryHasLegacyMarker(entry map[string]any) bool {
	if _, ok := entry[legacyMarkerOwnerField]; ok {
		return true
	}
	for key := range entry {
		if looksLikeLegacyMarkerIDField(key) {
			return true
		}
	}
	return false
}

func entryHasMarker(entry map[string]any) bool {
	if _, _, _, ok := ParseCommandSuffixMarker(entryCommand(entry)); ok {
		return true
	}
	return entryHasLegacyMarker(entry)
}

func TestMarkerSupport(t *testing.T) {
	claude := MarkerSupportFor(AgentClaude)
	if !claude.Supported || claude.Evidence != EvidenceObserved || claude.Note == "" {
		t.Fatalf("Claude suffix support = %+v", claude)
	}
	for _, want := range []string{"2026-09-04", "2.1.259", "fired", "2026-09-06", "2.1.260", "stripped", "command", "async", "timeout", "2026-09-07"} {
		if !strings.Contains(claude.Note, want) {
			t.Fatalf("Claude suffix note lacks %q: %q", want, claude.Note)
		}
	}
	codex := MarkerSupportFor(AgentCodex)
	if !codex.Supported || codex.Evidence != EvidenceConfirmedOSS || !strings.Contains(codex.Note, "0df39752cbc4b88d0194ec62bdb0d56fbda4b014") || !strings.Contains(codex.Note, "/bin/sh -lc") {
		t.Fatalf("Codex suffix support = %+v", codex)
	}
	pi := MarkerSupportFor(AgentPi)
	if pi.Supported || pi.Note == "" {
		t.Fatalf("pi marker support = %+v", pi)
	}
	if got := legacyMarkerIDField("my tool"); got != "x-my-tool-id" {
		t.Fatalf("legacyMarkerIDField = %q", got)
	}

	path := filepath.Join(t.TempDir(), "hooks.json")
	manager := ConfigManager{Agent: AgentCodex, SettingsPath: path, ToolName: "mytool", Invocation: "mytool", Hooks: []HookSpec{{Event: EventSessionStart, Command: "mytool start", ID: "start"}}}
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatalf("Codex configuration mutation failed: %v", err)
	}
	if plan.Style != MarkerStyleCommandSuffix {
		t.Fatalf("Codex auto style = %q", plan.Style)
	}
}

func TestDefaultOwnershipPredicateUnwrapsAssignmentsAndEnv(t *testing.T) {
	owns := DefaultOwnershipPredicate("mytool")
	for _, command := range []string{
		"DEBUG=1 /usr/local/bin/mytool hook",
		"env DEBUG=1 --unset OLD /usr/local/bin/mytool hook --flag",
		`env 'DEBUG=1' "/usr/local/bin/mytool" hook`,
		BuildCommandSuffixMarker("/usr/local/bin/mytool hook", "mytool", "hook"),
	} {
		if !owns(HookEntry{Command: command}) {
			t.Errorf("predicate did not recognize %q", command)
		}
	}
	for _, command := range []string{
		"/usr/local/bin/mytool-other hook",
		"env DEBUG=1 other-tool hook",
		"/usr/local/bin/mytool; other-tool hook",
	} {
		if owns(HookEntry{Command: command}) {
			t.Errorf("predicate falsely recognized %q", command)
		}
	}
}

func TestNoneStyleInstallPreservesSettingsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"permissions": map[string]any{"allow": []any{"Read"}},
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"matcher": "*",
				"hooks": []any{map[string]any{
					"type": "command", "command": "other-tool start", "vendor": map[string]any{"keep": true},
				}},
			}},
		},
	}, 0o600)

	manager := configManager(path, "/opt/mytool",
		HookSpec{Event: EventSessionStart, Command: "/opt/mytool start", ID: "start"},
		HookSpec{Event: EventStop, Command: "/opt/mytool stop", ID: "stop"},
	)
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges || len(plan.Added) != 2 || plan.Summary.Added != 2 {
		t.Fatalf("unexpected install plan: %+v", plan)
	}
	if plan.DiffNote != DiffKeyOrderNote || !strings.Contains(plan.Diff, "key-sorted") {
		t.Fatalf("plan does not describe its diff representation: %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	settings := readConfig(t, path)
	if !reflect.DeepEqual(settings["permissions"], map[string]any{"allow": []any{"Read"}}) {
		t.Fatalf("permissions changed: %#v", settings["permissions"])
	}
	entries := commandEntries(t, settings, "SessionStart")
	if len(entries) != 2 {
		t.Fatalf("SessionStart entries = %#v", entries)
	}
	var installed map[string]any
	for _, entry := range entries {
		if entry["command"] == "/opt/mytool start" {
			installed = entry
		}
	}
	if installed == nil || entryHasMarker(installed) {
		t.Fatalf("installed entry unexpectedly has markers: %#v", installed)
	}
	for _, entry := range entries {
		if entryHasMarker(entry) {
			t.Fatalf("Claude entry unexpectedly has marker fields: %#v", entry)
		}
		if entry["command"] == "other-tool start" && !reflect.DeepEqual(entry["vendor"], map[string]any{"keep": true}) {
			t.Fatalf("foreign entry lost its unknown field: %#v", entry)
		}
	}
	for _, candidate := range []string{path, plan.BackupPath} {
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			t.Fatalf("stat %s: %v", candidate, statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %s = %o, want 600", candidate, info.Mode().Perm())
		}
	}

	beforeSecond := stringMustRead(t, path)
	second, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if second.HasChanges || second.Summary.Added != 0 || second.Summary.Removed != 0 {
		t.Fatalf("second install is not idempotent: %+v", second)
	}
	if afterSecond := stringMustRead(t, path); afterSecond != beforeSecond {
		t.Fatalf("second install changed the file bytes:\nbefore=%q\nafter=%q", beforeSecond, afterSecond)
	}
}

func TestPredicateOnlyInstallConvergesStalePathWithoutAdoption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"matcher": "stop",
				"hooks": []any{map[string]any{
					"type": "command", "command": "/old/mytool stop --hand-edited", "async": false,
				}},
			}},
		},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{
		Event: EventStop, Command: "/new/mytool stop", ID: "stop",
	})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unmarked) != 0 {
		t.Fatalf("predicate-owned entry was reported as unmarked: %#v", plan.Unmarked)
	}
	if !plan.HasChanges || plan.Summary.Modified != 1 || len(plan.Added) != 1 || len(plan.Removed) != 1 {
		t.Fatalf("unexpected convergence plan: %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 || entries[0]["command"] != "/new/mytool stop --hand-edited" || entryHasMarker(entries[0]) {
		t.Fatalf("predicate convergence = %#v", entries)
	}
	beforeSecond := stringMustRead(t, path)
	second, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if second.HasChanges || len(second.Unmarked) != 0 {
		t.Fatalf("repeated predicate install is not a no-op: %+v", second)
	}
	if afterSecond := stringMustRead(t, path); afterSecond != beforeSecond {
		t.Fatalf("repeated predicate install changed bytes")
	}
}

func TestMarkerLossDoesNotTriggerAdoptionChurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	manager := configManager(path, "/opt/mytool",
		HookSpec{Event: EventSessionStart, Command: "/opt/mytool start", ID: "start"},
		HookSpec{Event: EventStop, Command: "/opt/mytool stop", ID: "stop"},
	)
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	settings := readConfig(t, path)
	for _, event := range []string{"SessionStart", "Stop"} {
		for _, entry := range commandEntries(t, settings, event) {
			entry[legacyMarkerOwnerField] = "mytool"
			entry[legacyMarkerIDField("mytool")] = event
		}
	}
	writeConfig(t, path, settings, 0o644)
	for _, event := range []string{"SessionStart", "Stop"} {
		for _, entry := range commandEntries(t, settings, event) {
			delete(entry, legacyMarkerOwnerField)
			delete(entry, legacyMarkerIDField("mytool"))
		}
	}
	writeConfig(t, path, settings, 0o644)

	before := stringMustRead(t, path)
	plan, err = manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges || len(plan.Unmarked) != 0 || plan.Summary.Added != 0 || plan.Summary.Removed != 0 {
		t.Fatalf("marker loss caused adoption churn: %+v", plan)
	}
	if after := stringMustRead(t, path); after != before {
		t.Fatalf("marker loss no-op changed file bytes")
	}
}

func TestVerifyGateUsesPredicateWithNoneStyle(t *testing.T) {
	policy := ownershipPolicy{
		toolName:    "mytool",
		predicate:   DefaultOwnershipPredicate("mytool"),
		markerStyle: MarkerStyleNone,
	}
	before := map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "/old/mytool stop"},
			map[string]any{"type": "command", "command": "/old/other-tool stop"},
		}}}},
	}

	added, err := cloneSettings(before)
	if err != nil {
		t.Fatal(err)
	}
	inner := added["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)
	inner = append(inner, map[string]any{"type": "command", "command": "/new/mytool start"})
	added["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"] = inner
	if err := verifyChangeSafe(before, added, policy, true); err != nil {
		t.Fatalf("predicate-owned addition rejected: %v", err)
	}

	removed, err := cloneSettings(before)
	if err != nil {
		t.Fatal(err)
	}
	removedInner := removed["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)
	removed["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"] = removedInner[1:]
	if err := verifyChangeSafe(before, removed, policy, false); err != nil {
		t.Fatalf("predicate-owned removal rejected: %v", err)
	}

	bad, err := cloneSettings(before)
	if err != nil {
		t.Fatal(err)
	}
	badInner := bad["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)
	badInner = append(badInner, map[string]any{
		"type": "command", "command": "/old/other-tool injected",
		legacyMarkerOwnerField: "mytool", legacyMarkerIDField("mytool"): "injected",
	})
	bad["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"] = badInner
	if err := verifyChangeSafe(before, bad, policy, true); err == nil {
		t.Fatal("marker-bearing non-predicate addition passed the predicate-only gate")
	}
}

func TestInstallConvergesLegacyMarkedEntryAndPreservesFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"matcher": "stop",
				"hooks": []any{map[string]any{
					"type": "command", "command": "/old/mytool stop --hand-edited", "async": false,
					legacyMarkerOwnerField: "mytool", legacyMarkerIDField("mytool"): "stop",
				}},
			}},
		},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{
		Event: EventStop, Command: "/new/mytool stop", ID: "stop",
	})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges || plan.Summary.Modified != 1 || len(plan.Added) != 1 || len(plan.Removed) != 1 {
		t.Fatalf("unexpected convergence plan: %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 || entries[0]["command"] != "/new/mytool stop --hand-edited" {
		t.Fatalf("converged entries = %#v", entries)
	}
	if entries[0]["matcher"] != nil {
		t.Fatal("matcher unexpectedly moved into hook entry")
	}
	if entryHasMarker(entries[0]) {
		t.Fatalf("converged entry unexpectedly has markers: %#v", entries[0])
	}
}

func TestPredicateOnlyUninstallRemovesExactlyPredicateOwnedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"permissions": map[string]any{"deny": []any{"Bash(rm *)"}},
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"matcher": "*", "hooks": []any{
				map[string]any{"type": "command", "command": "/old/mytool stop"},
				map[string]any{"type": "command", "command": "/old/mytool-other stop"},
				map[string]any{"type": "command", "command": "/old/other-tool stop"},
				map[string]any{"type": "prompt", "prompt": "foreign"},
			}}},
		},
	}, 0o644)
	manager := configManager(path, "")
	plan, err := manager.PlanUninstall()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unmarked) != 0 || !plan.HasChanges || len(plan.Removed) != 1 || plan.Removed[0].Command != "/old/mytool stop" {
		t.Fatalf("unexpected predicate uninstall plan: %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	settings := readConfig(t, path)
	if !reflect.DeepEqual(settings["permissions"], map[string]any{"deny": []any{"Bash(rm *)"}}) {
		t.Fatalf("permissions changed: %#v", settings["permissions"])
	}
	entries := commandEntries(t, settings, "Stop")
	if len(entries) != 3 {
		t.Fatalf("predicate uninstall removed the wrong entries: %#v", entries)
	}
	for _, entry := range entries {
		if entry["command"] == "/old/mytool stop" {
			t.Fatal("predicate-owned entry survived uninstall")
		}
	}
}

func TestMarkedIDWithChangedActionUsesDeclaredCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "/old/mytool old-action --manual", legacyMarkerOwnerField: "mytool", legacyMarkerIDField("mytool"): "stable"},
		}}}},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool new-action", ID: "stable"})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 || entries[0]["command"] != "/new/mytool new-action" {
		t.Fatalf("changed action was not declared command: %#v", entries)
	}
}

func TestInstallConvergenceUsesFirstOwnedWrapper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{
			map[string]any{"matcher": "first", "hooks": []any{
				map[string]any{"type": "command", "command": "/old/mytool stop", legacyMarkerOwnerField: "mytool", legacyMarkerIDField("mytool"): "old-1"},
			}},
			map[string]any{"matcher": "second", "hooks": []any{
				map[string]any{"type": "command", "command": "/older/mytool stop", legacyMarkerOwnerField: "mytool", legacyMarkerIDField("mytool"): "old-2"},
			}},
		}},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "new"})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	settings := readConfig(t, path)
	groups := settings["hooks"].(map[string]any)["Stop"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["matcher"] != "first" {
		t.Fatalf("groups after convergence = %#v", groups)
	}
	entries := commandEntries(t, settings, "Stop")
	if len(entries) != 1 || entries[0]["command"] != "/new/mytool stop" {
		t.Fatalf("entries after convergence = %#v", entries)
	}
}

func TestSuffixMarkerPolicyRequiresExplicitAdoptionAndProtectsForeignEntries(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": "/old/mytool stop --legacy"},
				map[string]any{"type": "command", "command": "/old/mytool stop", legacyMarkerOwnerField: "other", legacyMarkerIDField("mytool"): "foreign"},
				map[string]any{"type": "command", "command": "/old/mytool stop --other-marker", "x-other-tool-id": "foreign"},
			}}},
		},
	}
	policy := ownershipPolicy{
		toolName:    "mytool",
		predicate:   DefaultOwnershipPredicate("mytool"),
		markerStyle: MarkerStyleCommandSuffix,
	}
	if got := collectUnmarked(settings, policy); len(got) != 1 || got[0].Command != "/old/mytool stop --legacy" {
		t.Fatalf("unmarked report = %#v", got)
	}

	policy.adoptUnmarked = true
	after, err := cloneSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	desired := []desiredEntry{{canonical: EventStop, event: "Stop", command: "/new/mytool stop", id: "stop"}}
	if _, _, err := convergeEntries(after, policy, "/new/mytool", desired); err != nil {
		t.Fatal(err)
	}
	if err := verifyChangeSafe(settings, after, policy, true); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, after, "Stop")
	if len(entries) != 3 {
		t.Fatalf("entries after adoption = %#v", entries)
	}
	var adopted, foreign, otherMarked map[string]any
	for _, entry := range entries {
		clean, owner, id, marked := ParseCommandSuffixMarker(entryCommand(entry))
		switch {
		case marked && clean == "/new/mytool stop --legacy":
			adopted = entry
		case clean == "/old/mytool stop":
			foreign = entry
		case clean == "/old/mytool stop --other-marker":
			otherMarked = entry
		}
		if marked && clean == "/new/mytool stop --legacy" && (owner != "mytool" || id != "stop" || entryHasLegacyMarker(entry)) {
			t.Fatalf("adopted entry retained invalid metadata: %#v", entry)
		}
	}
	if adopted == nil {
		t.Fatalf("legacy entry was not explicitly adopted: %#v", entries)
	}
	if foreign == nil || foreign[legacyMarkerOwnerField] != "other" || otherMarked == nil {
		t.Fatalf("wrong-marker entry was touched: %#v / %#v", foreign, otherMarked)
	}

	beforeUninstall := after
	uninstall, err := cloneSettings(beforeUninstall)
	if err != nil {
		t.Fatal(err)
	}
	removed := removeOwnedEntries(uninstall, policy)
	if len(removed) != 1 || removed[0].Command != "/new/mytool stop --legacy #crossagent:v1:bXl0b29s:c3RvcA" {
		t.Fatalf("removed entries = %#v", removed)
	}
	if err := verifyChangeSafe(beforeUninstall, uninstall, policy, false); err != nil {
		t.Fatal(err)
	}
	entries = commandEntries(t, uninstall, "Stop")
	if len(entries) != 2 {
		t.Fatalf("uninstall touched the wrong-marker entries: %#v", entries)
	}
	for _, entry := range entries {
		clean, _, _, marked := ParseCommandSuffixMarker(entryCommand(entry))
		if marked && clean == "/new/mytool stop --legacy" {
			t.Fatal("adopted entry survived uninstall")
		}
	}
}

func TestCustomOwnershipPredicateIsCalledWithCopy(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "prompt", "prompt": "legacy"},
			}}},
		},
	}
	called := false
	policy := ownershipPolicy{
		toolName:    "mytool",
		markerStyle: MarkerStyleCommandSuffix,
		predicate: func(entry HookEntry) bool {
			called = true
			entry.Fields["mutated"] = true
			return entry.Fields["type"] == "prompt"
		},
	}
	unmarked := collectUnmarked(settings, policy)
	if !called || len(unmarked) != 1 {
		t.Fatalf("custom predicate/report = called %t, %#v", called, unmarked)
	}
	entry := commandEntries(t, settings, "SessionStart")[0]
	if entry["mutated"] != nil {
		t.Fatalf("predicate mutated settings: %#v", entry)
	}
}

func TestProbeRunsBeforeWriteAndIsInjectable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{}, 0o644)
	called := false
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	manager.Probe = &Probe{
		Command:       []string{"/new/mytool", "--self-check"},
		ExpectedToken: "ready",
		CommandRunner: func(_ context.Context, argv []string) ([]byte, error) {
			called = true
			if !reflect.DeepEqual(argv, []string{"/new/mytool", "--self-check"}) {
				t.Fatalf("probe argv = %#v", argv)
			}
			return []byte("ready\n"), nil
		},
	}
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("probe was not called")
	}

	path2 := filepath.Join(dir, "failed.json")
	writeConfig(t, path2, map[string]any{}, 0o644)
	manager.SettingsPath = path2
	manager.Probe = &Probe{
		Command: []string{"/new/mytool", "--self-check"}, ExpectedToken: "ready",
		CommandRunner: func(context.Context, []string) ([]byte, error) { return []byte("bad"), nil },
	}
	plan, err = manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err == nil {
		t.Fatal("failed probe was ignored")
	}
	settings := readConfig(t, path2)
	if len(settings) != 0 {
		t.Fatalf("failed probe changed settings: %#v", settings)
	}
}

func TestApplyRejectsPlanFromDifferentInvocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	manager.Invocation = "/other/mytool"
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err == nil {
		t.Fatal("plan was applied by a manager with a different invocation")
	}
	if len(readConfig(t, path)) != 0 {
		t.Fatal("different manager changed settings")
	}
}

func TestApplyRejectsFileDeletionAfterPlanning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err == nil {
		t.Fatal("plan was applied after the target was deleted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted target was recreated: %v", err)
	}
}

func TestApplyRejectsStalePlan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, path, map[string]any{"user": "changed"}, 0o644)
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err == nil {
		t.Fatal("stale plan was applied")
	}
	if got := readConfig(t, path)["user"]; got != "changed" {
		t.Fatalf("stale apply changed file: %#v", got)
	}
}

func TestInvalidHooksShapeIsNeverReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{"hooks": "user-owned"}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	if _, err := manager.PlanInstall(); err == nil {
		t.Fatal("invalid hooks shape was silently replaced")
	}
	if got := readConfig(t, path)["hooks"]; got != "user-owned" {
		t.Fatalf("invalid hooks shape changed: %#v", got)
	}

	path = filepath.Join(dir, "null-event.json")
	writeConfig(t, path, map[string]any{"hooks": map[string]any{"Stop": nil}}, 0o644)
	manager = configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	if _, err := manager.PlanInstall(); err == nil {
		t.Fatal("null event shape was silently replaced")
	}
	if got := readConfig(t, path)["hooks"].(map[string]any)["Stop"]; got != nil {
		t.Fatalf("null event shape changed: %#v", got)
	}

	path = filepath.Join(dir, "null-document.json")
	if err := os.WriteFile(path, []byte("null\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager = configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	if _, err := manager.PlanInstall(); err == nil {
		t.Fatal("null document was silently replaced")
	}
	if got := stringMustRead(t, path); got != "null\n" {
		t.Fatalf("null document changed: %q", got)
	}
}

func TestEmptyForeignWrapperIsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"matcher": "empty", "hooks": []any{}}}},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	groups := readConfig(t, path)["hooks"].(map[string]any)["Stop"].([]any)
	if len(groups) != 2 || groups[0].(map[string]any)["matcher"] != "empty" {
		t.Fatalf("empty wrapper was not preserved: %#v", groups)
	}
}

func TestVerifyGatePreservesForeignWrapperAndTopLevelValues(t *testing.T) {
	before := map[string]any{
		"user": "value",
		"hooks": map[string]any{"Stop": []any{map[string]any{
			"matcher": "*",
			"hooks":   []any{map[string]any{"type": "command", "command": "foreign stop"}},
		}}},
	}
	after, err := cloneSettings(before)
	if err != nil {
		t.Fatal(err)
	}
	after["user"] = "changed"
	if err := verifyChangeSafe(before, after, ownershipPolicy{toolName: "mytool", predicate: DefaultOwnershipPredicate("mytool"), markerStyle: MarkerStyleCommandSuffix}, true); err == nil {
		t.Fatal("gate accepted a top-level mutation")
	}
	after, _ = cloneSettings(before)
	hooks := after["hooks"].(map[string]any)
	groups := hooks["Stop"].([]any)
	groups[0].(map[string]any)["matcher"] = "changed"
	if err := verifyChangeSafe(before, after, ownershipPolicy{toolName: "mytool", predicate: DefaultOwnershipPredicate("mytool"), markerStyle: MarkerStyleCommandSuffix}, true); err == nil {
		t.Fatal("gate accepted a foreign wrapper mutation")
	}
}

func TestCommandSuffixMarkerRoundTripAndQuotedHashes(t *testing.T) {
	clean := `env DEBUG=1 '/opt/my tool' 'argument # stays quoted'`
	got := BuildCommandSuffixMarker(clean, "tool/name", "entry:id with spaces")
	if !strings.HasPrefix(got, clean+" #") {
		t.Fatalf("suffix = %q", got)
	}
	without, toolName, id, marked := ParseCommandSuffixMarker(got)
	if !marked || without != clean || toolName != "tool/name" || id != "entry:id with spaces" {
		t.Fatalf("parsed suffix = clean %q, tool %q, id %q, marked %t", without, toolName, id, marked)
	}
	stripped, didStrip := StripCommandSuffixMarker(got)
	if !didStrip || stripped != clean {
		t.Fatalf("stripped suffix = %q, stripped=%t", stripped, didStrip)
	}

	marker := strings.TrimPrefix(BuildCommandSuffixMarker("mytool", "mytool", "quoted"), "mytool ")
	for _, command := range []string{
		"mytool '" + marker + "'",
		`mytool "` + marker + `"`,
		"mytool foo\\ " + marker,
	} {
		if _, _, _, marked := ParseCommandSuffixMarker(command); marked {
			t.Errorf("quoted/escaped marker was detected in %q", command)
		}
	}
	if _, _, _, marked := ParseCommandSuffixMarker("mytool " + marker + " trailing"); marked {
		t.Fatal("non-trailing marker was detected")
	}
}

func TestMarkerStylesAndAutoResolution(t *testing.T) {
	tests := []struct {
		name       string
		style      MarkerStyle
		wantStyle  MarkerStyle
		wantSuffix bool
	}{
		{name: "explicit suffix", style: MarkerStyleCommandSuffix, wantStyle: MarkerStyleCommandSuffix, wantSuffix: true},
		{name: "explicit none", style: MarkerStyleNone, wantStyle: MarkerStyleNone},
		{name: "auto", style: MarkerStyleAuto, wantStyle: MarkerStyleCommandSuffix, wantSuffix: true},
		{name: "zero auto", wantStyle: MarkerStyleCommandSuffix, wantSuffix: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			manager := ConfigManager{
				Agent:        AgentClaude,
				SettingsPath: path,
				ToolName:     "mytool",
				Invocation:   "/opt/mytool",
				MarkerStyle:  tt.style,
				Hooks:        []HookSpec{{Event: EventSessionStart, Command: "/opt/mytool start", ID: "start"}},
			}
			plan, err := manager.PlanInstall()
			if err != nil {
				t.Fatal(err)
			}
			if plan.Style != tt.wantStyle {
				t.Fatalf("resolved style = %q, want %q", plan.Style, tt.wantStyle)
			}
			if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			entries := commandEntries(t, readConfig(t, path), "SessionStart")
			if len(entries) != 1 {
				t.Fatalf("entries = %#v", entries)
			}
			clean, owner, id, marked := ParseCommandSuffixMarker(entryCommand(entries[0]))
			if marked != tt.wantSuffix {
				t.Fatalf("suffix marked=%t, want %t: %#v", marked, tt.wantSuffix, entries[0])
			}
			if tt.wantSuffix && (clean != "/opt/mytool start" || owner != "mytool" || id != "start") {
				t.Fatalf("suffix marker = clean %q, owner %q, id %q", clean, owner, id)
			}
			if !tt.wantSuffix && entryCommand(entries[0]) != "/opt/mytool start" {
				t.Fatalf("none command = %q", entryCommand(entries[0]))
			}
			if entryHasLegacyMarker(entries[0]) {
				t.Fatalf("new entry has legacy field marker: %#v", entries[0])
			}
		})
	}

	codexPath := filepath.Join(t.TempDir(), "hooks.json")
	codex := ConfigManager{
		Agent:        AgentCodex,
		SettingsPath: codexPath,
		ToolName:     "mytool",
		Invocation:   "/opt/mytool",
		Hooks:        []HookSpec{{Event: EventSessionStart, Command: "/opt/mytool start", ID: "start"}},
	}
	plan, err := codex.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Style != MarkerStyleCommandSuffix {
		t.Fatalf("Codex auto style = %q", plan.Style)
	}

	if style, err := resolveMarkerStyle(MarkerStyleAuto, AgentPi); err != nil || style != MarkerStyleNone {
		t.Fatalf("pi auto style = %q, err=%v", style, err)
	}
	unsafe := ConfigManager{Agent: AgentPi, SettingsPath: filepath.Join(t.TempDir(), "settings.json"), ToolName: "mytool", Invocation: "mytool", MarkerStyle: MarkerStyleCommandSuffix}
	if _, err := unsafe.PlanInstall(); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe suffix plan error = %v", err)
	}
}

func TestLegacyFieldMarkerConvergesToSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{
				"type": "command", "command": "/old/mytool stop --hand-edited", "async": false,
				legacyMarkerOwnerField: "mytool", legacyMarkerIDField("mytool"): "stop",
			},
		}}}},
	}, 0o644)
	manager := ConfigManager{
		Agent:        AgentClaude,
		SettingsPath: path,
		ToolName:     "mytool",
		Invocation:   "/new/mytool",
		MarkerStyle:  MarkerStyleCommandSuffix,
		Hooks:        []HookSpec{{Event: EventStop, Command: "/new/mytool stop", ID: "stop"}},
	}
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges || plan.Summary.Modified != 1 {
		t.Fatalf("legacy-to-suffix plan = %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 {
		t.Fatalf("legacy-to-suffix entries = %#v", entries)
	}
	clean, owner, id, marked := ParseCommandSuffixMarker(entryCommand(entries[0]))
	if !marked || clean != "/new/mytool stop --hand-edited" || owner != "mytool" || id != "stop" || entryHasLegacyMarker(entries[0]) {
		t.Fatalf("legacy marker was not replaced by suffix: %#v", entries[0])
	}
}

func TestSuffixOwnershipConvergesAcrossStylesAndInvocationPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager := ConfigManager{
		Agent:        AgentClaude,
		SettingsPath: path,
		ToolName:     "mytool",
		Invocation:   "/old/mytool",
		MarkerStyle:  MarkerStyleCommandSuffix,
		Hooks:        []HookSpec{{Event: EventStop, Command: "/old/mytool stop", ID: "stop"}},
	}
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}

	manager.Invocation = "/new/mytool"
	manager.Hooks = []HookSpec{{Event: EventStop, Command: "/new/mytool stop", ID: "stop"}}
	plan, err = manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges || plan.Summary.Modified != 1 || len(plan.Added) != 1 || len(plan.Removed) != 1 {
		t.Fatalf("path convergence plan = %+v", plan)
	}
	if !strings.Contains(plan.Diff, BuildCommandSuffixMarker("/new/mytool stop", "mytool", "stop")) {
		t.Fatalf("path convergence diff lacks suffix: %s", plan.Diff)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 || entryCommand(entries[0]) != BuildCommandSuffixMarker("/new/mytool stop", "mytool", "stop") {
		t.Fatalf("path convergence entries = %#v", entries)
	}

	manager.MarkerStyle = MarkerStyleNone
	plan, err = manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges || plan.Summary.Modified != 1 {
		t.Fatalf("suffix-to-none plan = %+v", plan)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries = commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 1 || entryCommand(entries[0]) != "/new/mytool stop" {
		t.Fatalf("suffix-to-none entries = %#v", entries)
	}

	// None intentionally leaves no durable marker. Switching back to a
	// marker-authoritative style therefore requires the same explicit adoption
	// opt-in as any other legacy predicate match.
	manager.MarkerStyle = MarkerStyleCommandSuffix
	manager.AdoptUnmarked = true
	plan, err = manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasChanges || plan.Summary.Modified != 1 {
		t.Fatalf("none-to-suffix plan = %+v", plan)
	}
}

func TestVerifyGateAcceptsSuffixOwnershipAndProtectsForeignCommands(t *testing.T) {
	policy := ownershipPolicy{
		toolName:    "mytool",
		predicate:   DefaultOwnershipPredicate("mytool"),
		markerStyle: MarkerStyleCommandSuffix,
	}
	owned := map[string]any{"type": "command", "command": BuildCommandSuffixMarker("/old/mytool stop", "mytool", "old")}
	foreign := map[string]any{"type": "command", "command": BuildCommandSuffixMarker("/foreign/tool stop", "other-tool", "foreign")}
	before := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{owned, foreign}}}}}
	after, err := cloneSettings(before)
	if err != nil {
		t.Fatal(err)
	}
	inner := after["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)
	inner[0] = map[string]any{"type": "command", "command": BuildCommandSuffixMarker("/new/mytool stop", "mytool", "new")}
	after["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"] = inner
	if err := verifyChangeSafe(before, after, policy, true); err != nil {
		t.Fatalf("suffix-owned change rejected: %v", err)
	}

	bad, err := cloneSettings(before)
	if err != nil {
		t.Fatal(err)
	}
	badInner := bad["hooks"].(map[string]any)["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)
	badInner[1].(map[string]any)["command"] = BuildCommandSuffixMarker("/changed/foreign stop", "other-tool", "foreign")
	if err := verifyChangeSafe(before, bad, policy, true); err == nil {
		t.Fatal("foreign command mutation passed suffix verify gate")
	}
}

func TestClaudeSuffixSurvivesUnknownKeyStripping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager := ConfigManager{
		Agent:        AgentClaude,
		SettingsPath: path,
		ToolName:     "mytool",
		Invocation:   "/opt/mytool",
		MarkerStyle:  MarkerStyleCommandSuffix,
		Hooks:        []HookSpec{{Event: EventSessionStart, Command: "/opt/mytool start", ID: "start"}},
	}
	plan, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	settings := readConfig(t, path)
	for _, entry := range commandEntries(t, settings, "SessionStart") {
		entry["vendorUnknown"] = true
		entry[legacyMarkerOwnerField] = "mytool"
		entry[legacyMarkerIDField("mytool")] = "start"
	}
	writeConfig(t, path, settings, 0o644)

	// Simulate Claude's settings write: known hook fields survive, while
	// unknown fields (including legacy JSON markers) disappear. The suffix is
	// inside the known command field and must remain byte-for-byte intact.
	for _, entry := range commandEntries(t, settings, "SessionStart") {
		for key := range entry {
			if key != "type" && key != "command" && key != "async" && key != "timeout" {
				delete(entry, key)
			}
		}
	}
	writeConfig(t, path, settings, 0o644)
	before := stringMustRead(t, path)
	plan, err = manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges || plan.Summary.Added != 0 || plan.Summary.Removed != 0 || stringMustRead(t, path) != before {
		t.Fatalf("suffix install was not a no-op after key stripping: %+v", plan)
	}
}

func stringMustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

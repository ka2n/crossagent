package hooks

import (
	"context"
	"encoding/json"
	"errors"
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

func TestMarkerSupport(t *testing.T) {
	claude := MarkerSupportFor(AgentClaude)
	if !claude.Supported || !claude.UnknownKeysTolerated || claude.Evidence != EvidenceObserved {
		t.Fatalf("Claude marker support = %+v", claude)
	}
	codex := MarkerSupportFor(AgentCodex)
	if codex.Supported || codex.UnknownKeysTolerated {
		t.Fatalf("Codex marker support must remain unverified: %+v", codex)
	}
	pi := MarkerSupportFor(AgentPi)
	if pi.Supported || pi.Note == "" {
		t.Fatalf("pi marker support = %+v", pi)
	}
	if got := MarkerIDField("my tool"); got != "x-my-tool-id" {
		t.Fatalf("MarkerIDField = %q", got)
	}

	manager := ConfigManager{Agent: AgentCodex, ToolName: "mytool", Invocation: "mytool"}
	if _, err := manager.PlanInstall(); err == nil || !strings.Contains(err.Error(), "marker") {
		t.Fatalf("Codex configuration mutation was accepted: %v", err)
	}
}

func TestDefaultOwnershipPredicateUnwrapsAssignmentsAndEnv(t *testing.T) {
	owns := DefaultOwnershipPredicate("mytool")
	for _, command := range []string{
		"DEBUG=1 /usr/local/bin/mytool hook",
		"env DEBUG=1 --unset OLD /usr/local/bin/mytool hook --flag",
		`env 'DEBUG=1' "/usr/local/bin/mytool" hook`,
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

func TestInstallUsesMarkersPreservesSettingsAndIsIdempotent(t *testing.T) {
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
	var marked map[string]any
	for _, entry := range entries {
		if entry["command"] == "/opt/mytool start" {
			marked = entry
		}
	}
	if marked == nil || marked[MarkerOwnerField] != "mytool" || marked[MarkerIDField("mytool")] != "start" {
		t.Fatalf("installed entry lacks markers: %#v", marked)
	}
	for _, entry := range entries {
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

	second, err := manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if second.HasChanges || second.Summary.Added != 0 || second.Summary.Removed != 0 {
		t.Fatalf("second install is not idempotent: %+v", second)
	}
}

func TestInstallConvergesMarkedEntryByIDAndPreservesFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"matcher": "stop",
				"hooks": []any{map[string]any{
					"type": "command", "command": "/old/mytool stop --hand-edited", "async": false,
					MarkerOwnerField: "mytool", MarkerIDField("mytool"): "stop",
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
	if entries[0][MarkerOwnerField] != "mytool" || entries[0][MarkerIDField("mytool")] != "stop" {
		t.Fatalf("markers changed unexpectedly: %#v", entries[0])
	}
}

func TestMarkedIDWithChangedActionUsesDeclaredCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "/old/mytool old-action --manual", MarkerOwnerField: "mytool", MarkerIDField("mytool"): "stable"},
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
				map[string]any{"type": "command", "command": "/old/mytool stop", MarkerOwnerField: "mytool", MarkerIDField("mytool"): "old-1"},
			}},
			map[string]any{"matcher": "second", "hooks": []any{
				map[string]any{"type": "command", "command": "/older/mytool stop", MarkerOwnerField: "mytool", MarkerIDField("mytool"): "old-2"},
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

func TestUnmarkedFallbackRequiresExplicitAdoptionAndWrongMarkerIsForeign(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": "/old/mytool stop --legacy"},
				map[string]any{"type": "command", "command": "/old/mytool stop", MarkerOwnerField: "other", MarkerIDField("mytool"): "foreign"},
				map[string]any{"type": "command", "command": "/old/mytool stop --other-marker", "x-other-tool-id": "foreign"},
			}}},
		},
	}, 0o644)
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventStop, Command: "/new/mytool stop", ID: "stop"})
	plan, err := manager.PlanInstall()
	var ownershipErr *UnmarkedOwnershipError
	if !errors.As(err, &ownershipErr) {
		t.Fatalf("PlanInstall error = %v, want UnmarkedOwnershipError", err)
	}
	if len(plan.Unmarked) != 1 || len(ownershipErr.Entries) != 1 {
		t.Fatalf("unmarked report = %#v / %#v", plan.Unmarked, ownershipErr.Entries)
	}
	if got := stringMustRead(t, path); got == "" {
		t.Fatal("settings unexpectedly disappeared")
	}

	manager.AdoptUnmarked = true
	plan, err = manager.PlanInstall()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries := commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 3 {
		t.Fatalf("entries after adoption = %#v", entries)
	}
	var adopted, foreign, otherMarked map[string]any
	for _, entry := range entries {
		if entry["command"] == "/new/mytool stop --legacy" {
			adopted = entry
		}
		if entry["command"] == "/old/mytool stop" {
			foreign = entry
		}
		if entry["command"] == "/old/mytool stop --other-marker" {
			otherMarked = entry
		}
	}
	if adopted == nil || adopted[MarkerOwnerField] != "mytool" || adopted[MarkerIDField("mytool")] != "stop" {
		t.Fatalf("legacy entry was not explicitly adopted: %#v", adopted)
	}
	if foreign == nil || foreign[MarkerOwnerField] != "other" || otherMarked == nil {
		t.Fatalf("wrong-marker entry was touched: %#v / %#v", foreign, otherMarked)
	}

	uninstall := configManager(path, "")
	uninstall.Ownership = func(HookEntry) bool { return true }
	uninstall.AdoptUnmarked = false
	uninstallPlan, err := uninstall.PlanUninstall()
	if err != nil {
		t.Fatal(err)
	}
	if !uninstallPlan.HasChanges || len(uninstallPlan.Removed) != 1 {
		t.Fatalf("uninstall did not find the marked entry: %+v", uninstallPlan)
	}
	if err := uninstall.Apply(context.Background(), uninstallPlan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	entries = commandEntries(t, readConfig(t, path), "Stop")
	if len(entries) != 2 {
		t.Fatalf("uninstall touched the wrong-marker entries: %#v", entries)
	}
	for _, entry := range entries {
		if entry["command"] == "/new/mytool stop --legacy" {
			t.Fatal("adopted entry survived uninstall")
		}
	}
}

func TestCustomOwnershipPredicateIsCalledWithCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeConfig(t, path, map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"hooks": []any{
				map[string]any{"type": "prompt", "prompt": "legacy"},
			}}},
		},
	}, 0o644)
	called := false
	manager := configManager(path, "/new/mytool", HookSpec{Event: EventSessionStart, Command: "/new/mytool start", ID: "start"})
	manager.Ownership = func(entry HookEntry) bool {
		called = true
		entry.Fields["mutated"] = true
		return entry.Fields["type"] == "prompt"
	}
	plan, err := manager.PlanInstall()
	if err == nil {
		t.Fatal("unmarked custom match was silently adopted")
	}
	if !called || len(plan.Unmarked) != 1 {
		t.Fatalf("custom predicate/report = called %t, %#v, err %v", called, plan.Unmarked, err)
	}
	if got := commandEntries(t, readConfig(t, path), "SessionStart")[0]; got["mutated"] != nil {
		t.Fatalf("predicate mutated settings: %#v", got)
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
	if err := verifyChangeSafe(before, after, ownershipPolicy{toolName: "mytool", predicate: DefaultOwnershipPredicate("mytool"), markerEnabled: true}, true); err == nil {
		t.Fatal("gate accepted a top-level mutation")
	}
	after, _ = cloneSettings(before)
	hooks := after["hooks"].(map[string]any)
	groups := hooks["Stop"].([]any)
	groups[0].(map[string]any)["matcher"] = "changed"
	if err := verifyChangeSafe(before, after, ownershipPolicy{toolName: "mytool", predicate: DefaultOwnershipPredicate("mytool"), markerEnabled: true}, true); err == nil {
		t.Fatal("gate accepted a foreign wrapper mutation")
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

package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ka2n/crossagent/agent"
)

// DiffKeyOrderNote explains the representation detail behind settings diffs.
const DiffKeyOrderNote = "Writing re-encodes JSON with sorted object keys; both diff sides are key-sorted, so the diff shows value changes rather than hand-written key order."

// ChangeOperation identifies the direction of a ChangePlan.
type ChangeOperation string

const (
	// Install adds or converges the caller's hooks.
	Install ChangeOperation = operationInstall
	// Uninstall removes the caller's hooks.
	Uninstall ChangeOperation = operationUninstall
)

// EntryChange describes one hook entry added or removed by a plan. Command is
// the actual command spelling in the JSON diff, including a suffix when the
// resolved marker style is CommandSuffix.
type EntryChange struct {
	Event   string
	Command string
	ID      string
}

// EventChange summarizes the entry-level effect for one event. Modified is a
// subset count that pairs an addition and removal with the same action; it is
// intentionally also included in Added and Removed.
type EventChange struct {
	Event    string
	Existing int
	Kept     int
	Added    int
	Removed  int
	Modified int
	IsNew    bool
}

// ChangeSummary is a deterministic, entry-level summary of a plan.
type ChangeSummary struct {
	Added    int
	Removed  int
	Modified int
	Events   []EventChange
}

// ChangePlan contains all data a caller needs to show and confirm a
// configuration change. The manager does not print or prompt.
type ChangePlan struct {
	Path       string
	Agent      agent.Name
	Operation  ChangeOperation
	FileExists bool
	FileSize   int64
	BackupPath string
	// Style is the resolved ownership style used to build the plan. It is
	// useful when MarkerStyle was left at its auto default.
	Style      MarkerStyle
	HasChanges bool
	Added      []EntryChange
	Removed    []EntryChange
	Unmarked   []UnmarkedEntry
	Summary    ChangeSummary
	Diff       string
	// DiffNote explains why the rendered JSON diff may show key-order changes.
	DiffNote string

	path        string
	operation   ChangeOperation
	agent       agent.Name
	toolName    string
	invocation  string
	markerStyle MarkerStyle
	before      map[string]any
	beforeExist bool
	after       map[string]any
}

// Changed reports whether applying the plan would replace the target file.
func (p ChangePlan) Changed() bool {
	return p.HasChanges
}

func (m ConfigManager) plan(operation ChangeOperation) (ChangePlan, error) {
	c, err := m.config()
	if err != nil {
		return ChangePlan{}, err
	}
	path, err := c.settingsPath()
	if err != nil {
		return ChangePlan{}, err
	}
	before, err := loadSettings(path)
	if err != nil {
		return ChangePlan{}, err
	}
	plan := ChangePlan{
		Path:        path,
		Agent:       c.agent,
		Operation:   operation,
		BackupPath:  backupPath(path, c.toolName),
		Style:       c.markerStyle,
		path:        path,
		operation:   operation,
		agent:       c.agent,
		toolName:    c.toolName,
		invocation:  c.invocation,
		markerStyle: c.markerStyle,
		before:      before,
	}
	if info, statErr := os.Stat(path); statErr == nil {
		plan.FileExists = true
		plan.beforeExist = true
		plan.FileSize = info.Size()
	} else if !os.IsNotExist(statErr) {
		return plan, fmt.Errorf("stat %s: %w", path, statErr)
	}

	policy := c.policy()
	plan.Unmarked = collectUnmarked(before, policy)
	if len(plan.Unmarked) > 0 && !c.adoptUnmarked {
		return plan, &UnmarkedOwnershipError{ToolName: c.toolName, Entries: cloneUnmarked(plan.Unmarked)}
	}

	after, err := cloneSettings(before)
	if err != nil {
		return plan, err
	}
	var order []string
	switch operation {
	case operationInstall:
		desired, desiredOrder, desiredErr := c.desired()
		if desiredErr != nil {
			return plan, desiredErr
		}
		order = desiredOrder
		if conflicts := asyncSyncConflicts(before, policy, desired); len(conflicts) > 0 {
			return plan, fmt.Errorf("refusing to manage %s: an owned hook is marked async but is managed as a synchronous hook whose stdout is a protocol; remove async or explicitly repair it first (%s)", path, formatUnmarked(conflicts))
		}
		if len(desired) == 0 {
			// An empty install declaration is a no-op. Removal is explicit via
			// PlanUninstall, rather than an accidental consequence of a nil
			// desired slice.
		} else if hasOwnedEntries(before, policy) {
			if _, _, err := convergeEntries(after, policy, desired); err != nil {
				return plan, err
			}
		} else if err := mergeDesired(after, desired, policy); err != nil {
			return plan, err
		}
		if err := verifyChangeSafe(before, after, policy, true); err != nil {
			return plan, fmt.Errorf("refusing to plan install: %w", err)
		}
	case operationUninstall:
		removeOwnedEntries(after, policy)
		if err := verifyChangeSafe(before, after, policy, false); err != nil {
			return plan, fmt.Errorf("refusing to plan uninstall: %w", err)
		}
	default:
		return plan, fmt.Errorf("unknown change operation %q", operation)
	}

	plan.after = after
	beforeHooks, _ := hooksObject(before)
	afterHooks, _ := hooksObject(after)
	plan.Added = entryChanges(diffEntriesWithTool(beforeHooks, afterHooks, c.toolName))
	plan.Removed = entryChanges(diffEntriesWithTool(afterHooks, beforeHooks, c.toolName))
	plan.Summary = summarizeChange(before, after, order, c.toolName)
	beforeLines, err := settingsLines(before)
	if err != nil {
		return plan, err
	}
	afterLines, err := settingsLines(after)
	if err != nil {
		return plan, err
	}
	plan.Diff = unifiedDiff(beforeLines, afterLines,
		fmt.Sprintf("%s (now, key-sorted)", path),
		fmt.Sprintf("%s (after %s, key-sorted)", path, operation),
		3)
	plan.DiffNote = DiffKeyOrderNote
	plan.HasChanges = plan.Diff != ""
	return plan, nil
}

func hasOwnedEntries(settings map[string]any, policy ownershipPolicy) bool {
	hooks := hooksSection(settings, false)
	if hooks == nil {
		return false
	}
	for _, event := range sortedKeys(hooks) {
		groups, _ := hooks[event].([]any)
		for _, rawGroup := range groups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				continue
			}
			inner, _ := group["hooks"].([]any)
			for _, rawHook := range inner {
				hook, ok := rawHook.(map[string]any)
				if !ok {
					continue
				}
				owned, unmarked := classifyEntry(event, hook, policy)
				if owned || (unmarked && policy.adoptUnmarked) {
					return true
				}
			}
		}
	}
	return false
}

func cloneUnmarked(entries []UnmarkedEntry) []UnmarkedEntry {
	return append([]UnmarkedEntry(nil), entries...)
}

func formatUnmarked(entries []UnmarkedEntry) string {
	if len(entries) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.Event+": "+entry.Command)
	}
	return strings.Join(parts, "; ")
}

func diffEntriesWithTool(from, to map[string]any, toolName string) []entryDelta {
	var out []entryDelta
	for _, delta := range diffEntries(from, to) {
		delta.Command, delta.ID = entryDetails(delta.Entry, delta.Event, toolName)
		out = append(out, delta)
	}
	return out
}

func entryChanges(deltas []entryDelta) []EntryChange {
	out := make([]EntryChange, 0, len(deltas))
	for _, delta := range deltas {
		out = append(out, EntryChange{Event: delta.Event, Command: delta.Command, ID: delta.ID})
	}
	return out
}

func sameEntryChange(left, right entryDelta) bool {
	if left.ID != "" && left.ID == right.ID {
		return true
	}
	leftCommand, _ := StripCommandSuffixMarker(left.Command)
	rightCommand, _ := StripCommandSuffixMarker(right.Command)
	if strings.TrimSpace(leftCommand) != "" && leftCommand == rightCommand {
		return true
	}
	leftAction := actionKey(left.Command)
	return leftAction != "" && leftAction == actionKey(right.Command)
}

func summarizeChange(before, after map[string]any, eventOrder []string, toolName string) ChangeSummary {
	beforeHooks, _ := hooksObject(before)
	afterHooks, _ := hooksObject(after)
	added := diffEntriesWithTool(beforeHooks, afterHooks, toolName)
	removed := diffEntriesWithTool(afterHooks, beforeHooks, toolName)

	perEvent := map[string]*EventChange{}
	get := func(event string) *EventChange {
		if current, ok := perEvent[event]; ok {
			return current
		}
		current := &EventChange{Event: event}
		perEvent[event] = current
		return current
	}
	for _, event := range sortedKeys(beforeHooks) {
		wrappers, _ := parseWrappers(beforeHooks[event])
		entryCount := 0
		for _, count := range entryCounts(wrappers) {
			entryCount += count
		}
		get(event).Existing = entryCount
	}
	for _, event := range sortedKeys(afterHooks) {
		entry := get(event)
		if _, existed := beforeHooks[event]; !existed {
			entry.IsNew = true
		}
	}
	for _, delta := range added {
		get(delta.Event).Added++
	}
	for _, delta := range removed {
		get(delta.Event).Removed++
	}
	pairedAdd := make([]bool, len(added))
	for _, removedDelta := range removed {
		for index, addedDelta := range added {
			if pairedAdd[index] || addedDelta.Event != removedDelta.Event {
				continue
			}
			if !sameEntryChange(addedDelta, removedDelta) {
				continue
			}
			pairedAdd[index] = true
			get(removedDelta.Event).Modified++
			break
		}
	}

	out := ChangeSummary{}
	for _, event := range sortedEventKeys(perEvent, eventOrder) {
		entry := perEvent[event]
		entry.Kept = entry.Existing - entry.Removed
		if entry.Kept < 0 {
			entry.Kept = 0
		}
		out.Added += entry.Added
		out.Removed += entry.Removed
		out.Modified += entry.Modified
		out.Events = append(out.Events, *entry)
	}
	return out
}

func sortedEventKeys(events map[string]*EventChange, preferred []string) []string {
	var ordered, others []string
	seen := map[string]bool{}
	for _, event := range preferred {
		if _, ok := events[event]; ok && !seen[event] {
			ordered = append(ordered, event)
			seen[event] = true
		}
	}
	for event := range events {
		if !seen[event] {
			others = append(others, event)
		}
	}
	sort.Strings(others)
	return append(ordered, others...)
}

func settingsLines(settings map[string]any) ([]string, error) {
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	return splitLines(string(body)), nil
}

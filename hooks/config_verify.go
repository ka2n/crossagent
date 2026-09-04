package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// hookWrapper is one element of an event's array: normally an object with a
// "hooks" array plus other fields such as "matcher". A non-conforming value
// is kept as raw and compared whole because this package has no business
// reasoning about a shape it did not write.
type hookWrapper struct {
	conforming bool
	fields     map[string]any
	entries    []any
	raw        any
}

func hooksObject(settings map[string]any) (map[string]any, bool) {
	raw, ok := settings["hooks"]
	if !ok {
		return map[string]any{}, true
	}
	m, ok := raw.(map[string]any)
	return m, ok
}

func parseWrappers(value any) ([]hookWrapper, bool) {
	if value == nil {
		return nil, true
	}
	groups, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]hookWrapper, 0, len(groups))
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			out = append(out, hookWrapper{raw: rawGroup})
			continue
		}
		inner, ok := group["hooks"].([]any)
		if !ok {
			out = append(out, hookWrapper{raw: rawGroup})
			continue
		}
		fields := make(map[string]any, len(group))
		for key, value := range group {
			if key != "hooks" {
				fields[key] = value
			}
		}
		out = append(out, hookWrapper{conforming: true, fields: fields, entries: inner, raw: rawGroup})
	}
	return out, true
}

func canonJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(body)
}

func entryCounts(wrappers []hookWrapper) map[string]int {
	counts := map[string]int{}
	for _, wrapper := range wrappers {
		for _, entry := range wrapper.entries {
			counts[canonJSON(entry)]++
		}
	}
	return counts
}

type entryDelta struct {
	Event   string
	Entry   any
	Command string
	ID      string
	JSON    string
}

func diffEntries(from, to map[string]any) []entryDelta {
	var out []entryDelta
	for _, event := range sortedKeys(to) {
		toWrappers, ok := parseWrappers(to[event])
		if !ok {
			continue
		}
		fromWrappers, ok := parseWrappers(from[event])
		if !ok {
			fromWrappers = nil
		}
		remaining := entryCounts(fromWrappers)
		for _, wrapper := range toWrappers {
			for _, entry := range wrapper.entries {
				key := canonJSON(entry)
				if remaining[key] > 0 {
					remaining[key]--
					continue
				}
				command, id := entryDetails(entry, event, "")
				out = append(out, entryDelta{Event: event, Entry: entry, Command: command, ID: id, JSON: key})
			}
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func checkOutsideHooks(before, after map[string]any) error {
	strip := func(settings map[string]any) map[string]any {
		out := make(map[string]any, len(settings))
		for key, value := range settings {
			if key != "hooks" {
				out[key] = value
			}
		}
		return out
	}
	beforeOther, afterOther := strip(before), strip(after)
	if reflect.DeepEqual(beforeOther, afterOther) {
		return nil
	}
	for _, key := range sortedKeys(beforeOther) {
		value, ok := afterOther[key]
		if !ok {
			return fmt.Errorf("the key %q would be removed", key)
		}
		if !reflect.DeepEqual(beforeOther[key], value) {
			return fmt.Errorf("the key %q would change from %s to %s", key, canonJSON(beforeOther[key]), canonJSON(value))
		}
	}
	for _, key := range sortedKeys(afterOther) {
		if _, ok := beforeOther[key]; !ok {
			return fmt.Errorf("the key %q would be added outside \"hooks\"", key)
		}
	}
	return errors.New(`something outside "hooks" would change`)
}

func wrapperMatch(want hookWrapper, candidates []hookWrapper, used []bool) (int, error) {
	fieldsMatched := -1
	var missing string
	for i, candidate := range candidates {
		if used[i] {
			continue
		}
		if !want.conforming || !candidate.conforming {
			if want.conforming == candidate.conforming && reflect.DeepEqual(want.raw, candidate.raw) {
				return i, nil
			}
			continue
		}
		if !reflect.DeepEqual(want.fields, candidate.fields) {
			continue
		}
		if fieldsMatched < 0 {
			fieldsMatched = i
		}
		have := entryCounts([]hookWrapper{candidate})
		lost := ""
		for _, entry := range want.entries {
			key := canonJSON(entry)
			if have[key] > 0 {
				have[key]--
				continue
			}
			lost = key
			break
		}
		if lost == "" {
			return i, nil
		}
		if missing == "" {
			missing = lost
		}
	}
	if fieldsMatched >= 0 && missing != "" {
		return -1, fmt.Errorf("the hook entry %s is no longer there", missing)
	}
	if !want.conforming {
		return -1, fmt.Errorf("the entry %s is no longer there", canonJSON(want.raw))
	}
	return -1, fmt.Errorf("no wrapper with the fields %s survives, so the entries under it (%s) would be lost or moved",
		canonJSON(want.fields), canonJSON(want.entries))
}

func verifyChangeSafe(before, after map[string]any, policy ownershipPolicy, allowAdd bool) error {
	if strings.TrimSpace(policy.toolName) == "" {
		return errors.New("cannot verify the change without an ownership tool name")
	}
	if err := checkOutsideHooks(before, after); err != nil {
		return err
	}
	beforeHooks, beforeOK := hooksObject(before)
	afterHooks, afterOK := hooksObject(after)
	if !beforeOK || !afterOK {
		if !reflect.DeepEqual(before["hooks"], after["hooks"]) {
			return errors.New(`the "hooks" key is not an object and its value would change`)
		}
		return nil
	}

	beforeForeign, err := foreignHooks(before, policy)
	if err != nil {
		return err
	}
	afterForeign, err := foreignHooks(after, policy)
	if err != nil {
		return err
	}
	for _, event := range sortedKeys(beforeForeign) {
		if err := checkEventPreserved(event, beforeForeign[event], afterForeign[event]); err != nil {
			return err
		}
	}

	for _, added := range diffEntries(beforeHooks, afterHooks) {
		if !allowAdd {
			return fmt.Errorf("%s would gain the entry %s, but uninstall must only remove", added.Event, added.JSON)
		}
		if !addedEntryOwned(added.Entry, added.Command, policy) {
			return fmt.Errorf("%s would gain the entry %s, which is not a marked %s hook", added.Event, added.JSON, policy.toolName)
		}
	}
	for _, removed := range diffEntries(afterHooks, beforeHooks) {
		marked, unmarked := deltaOwnership(removed.Entry, removed.Event, policy)
		if !marked && !(unmarked && policy.adoptUnmarked) {
			return fmt.Errorf("%s would lose the entry %s, which is not an explicitly owned %s hook", removed.Event, removed.JSON, policy.toolName)
		}
	}
	return nil
}

func addedEntryOwned(raw any, command string, policy ownershipPolicy) bool {
	entry, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	_, marked, hasMarker := markerFor(entry, policy.toolName)
	if marked {
		return true
	}
	if hasMarker {
		return false
	}
	if policy.markerEnabled {
		return false
	}
	if policy.predicate == nil {
		return false
	}
	return policy.predicate(HookEntry{Command: command, Fields: cloneHookEntry(entry)})
}

func deltaOwnership(raw any, event string, policy ownershipPolicy) (marked, unmarked bool) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return false, false
	}
	return classifyEntry(event, entry, policy)
}

func foreignHooks(settings map[string]any, policy ownershipPolicy) (map[string]any, error) {
	copied, err := cloneSettings(settings)
	if err != nil {
		return nil, err
	}
	removeOwnedEntries(copied, policy)
	hooks, ok := hooksObject(copied)
	if !ok {
		return map[string]any{}, nil
	}
	return hooks, nil
}

func checkEventPreserved(event string, want, have any) error {
	wantWrappers, ok := parseWrappers(want)
	if !ok {
		if !reflect.DeepEqual(want, have) {
			return fmt.Errorf("%s: its value is not an array of hook groups and would change from %s to %s", event, canonJSON(want), canonJSON(have))
		}
		return nil
	}
	if len(wantWrappers) == 0 {
		return nil
	}
	haveWrappers, ok := parseWrappers(have)
	if !ok {
		return fmt.Errorf("%s: its value would become %s, which is not an array of hook groups", event, canonJSON(have))
	}
	if len(haveWrappers) == 0 {
		return fmt.Errorf("%s: the whole event would be dropped, losing %s", event, canonJSON(want))
	}
	used := make([]bool, len(haveWrappers))
	for _, wrapper := range wantWrappers {
		index, err := wrapperMatch(wrapper, haveWrappers, used)
		if err != nil {
			return fmt.Errorf("%s: %w", event, err)
		}
		used[index] = true
	}
	return nil
}

func entryDetails(raw any, event, toolName string) (command, id string) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return "", ""
	}
	command = entryCommand(entry)
	if toolName != "" {
		id, _, _ = markerFor(entry, toolName)
	}
	return command, id
}

func reflectSettingsEqual(left, right map[string]any) bool {
	return reflect.DeepEqual(left, right)
}

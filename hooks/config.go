package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ka2n/crossagent/agent"
	"github.com/ka2n/crossagent/paths"
)

// HookSpec declares one hook a caller wants to manage. Event is expressed in
// the canonical vocabulary from this package; ConfigManager maps it to the
// exact spelling accepted by the selected agent. Command is the clean command
// to execute; ConfigManager adds the selected ownership suffix, if any. ID is
// a stable per-entry identifier. Supplying an ID is recommended, especially
// when one event has multiple commands; a deterministic event/action ID is
// generated when it is omitted.
type HookSpec struct {
	Event   Event
	Command string
	ID      string
}

// HookEntry is the read-only view passed to a Matcher. Command is the clean
// command with a valid suffix marker removed; Fields is a copy of the complete
// raw hook-entry object, including unknown vendor fields.
type HookEntry struct {
	Event   string
	Command string
	Fields  map[string]any
}

// UnmarkedEntry describes a command that matches the fallback ownership
// matcher but carries no valid suffix or legacy marker. Such entries are
// reported when command-suffix style is selected and are never adopted
// silently.
type UnmarkedEntry struct {
	Event   string
	Command string
}

// UnmarkedOwnershipError reports legacy-looking entries that require explicit
// adoption before they can be removed, rewritten, or treated as installed by
// a command-suffix-style manager. None-style managers do not produce this
// error.
type UnmarkedOwnershipError struct {
	ToolName string
	Entries  []UnmarkedEntry
}

func (e *UnmarkedOwnershipError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "looks like %s's hook but is unmarked; set AdoptUnmarked to true to adopt it", e.ToolName)
	for _, entry := range e.Entries {
		fmt.Fprintf(&b, "\n  %s: %s", entry.Event, entry.Command)
	}
	return b.String()
}

// Probe describes a self-check command that proves the exact hook executable
// is usable before a configuration write. Command is an argv vector for the
// clean executable invocation and is run directly, never through a shell. The
// ownership suffix belongs only in the settings command string; it must not be
// added to Probe.Command. The caller supplies ExpectedToken because only the
// caller knows what proves its binary works.
type Probe struct {
	Command       []string
	ExpectedToken string
	Timeout       time.Duration
	CommandRunner ProbeRunner
}

// ProbeRunner executes a complete argv vector for a Probe. It is injectable so
// tests never need to execute a real integration binary.
type ProbeRunner func(context.Context, []string) ([]byte, error)

// ApplyOptions controls the side effects of applying a prepared plan.
type ApplyOptions struct {
	// SkipProbe bypasses the configured self-check. This should be explicit:
	// an unverified command can make every installed hook fail.
	SkipProbe bool
}

// ConfigManager plans and applies changes to a JSON hook configuration file.
// MarkerStyle selects command-suffix ownership, matcher-only ownership, or the
// auto default. Auto writes a suffix when MarkerSupportFor says hook commands
// are shell-executed and otherwise writes no marker. The old JSON field
// markers are never written; they are recognized only as read-only legacy
// ownership metadata and are removed while converging an owned entry.
// Matcher is the fallback ownership vocabulary for unmarked entries; its zero
// value is MatchBasename(ToolName). Convergence writes the declared
// HookSpec.Command (plus the selected ownership suffix) rather than preserving
// wrapper text or hand-edited command flags. In particular, an entry matched
// through MatchEnvWrapped can lose its wrapper unless the wrapper is declared
// in HookSpec.Command. The plan diff shows that replacement.
// Claude's settings.json and Codex's hooks.json are supported; pi has no
// declarative hook file. The manager never prompts or prints. Call
// PlanInstall/PlanUninstall, show the returned data, obtain any user
// confirmation in the caller, then call Apply.
type ConfigManager struct {
	// Resolver supplies home and environment lookup behavior for scope paths.
	Resolver paths.Resolver
	// Agent is the canonical agent name. Configuration mutation accepts
	// Claude and Codex; pi has no declarative hook file.
	Agent agent.Name
	// Scope selects the user, project, or local configuration path. Empty
	// means paths.ScopeUser.
	Scope paths.Scope
	// CWD is used for project and local scope resolution.
	CWD string
	// SettingsPath overrides Resolver and Scope. It is useful for tests and
	// for one-off vendor configuration locations.
	SettingsPath string

	// ToolName is the stable ownership identifier, for example "mytool".
	ToolName string
	// Invocation is the current executable spelling used as argv[0] in new
	// command strings. It may be a shell-quoted path, but must name ToolName.
	Invocation string
	// Hooks declares the desired canonical events and clean command strings.
	Hooks []HookSpec

	// MarkerStyle controls ownership recording. The zero value and
	// MarkerStyleAuto select the per-agent default. MarkerStyleCommandSuffix
	// is the command-comment marker; MarkerStyleNone writes no marker and uses
	// the matcher for otherwise unmarked entries. Existing valid markers stay
	// authoritative while they are converged. The old JSON field-marker style
	// is intentionally not exposed.
	MarkerStyle MarkerStyle

	// Matcher recognizes entries when no valid ownership marker is available.
	// A nil matcher uses MatchBasename(ToolName), which examines only argv[0]
	// and does not unwrap assignments or env. With command-suffix style, matches
	// without a marker require AdoptUnmarked; with none style, unmarked matches
	// are owned directly. MatchEnvWrapped is an explicit opt-in: convergence
	// still writes HookSpec.Command verbatim rather than preserving a recognized
	// wrapper.
	Matcher Matcher
	// AdoptUnmarked is the explicit safety opt-in for fallback-only matches when
	// command-suffix markers are selected. It has no effect with none style.
	AdoptUnmarked bool
	// Probe is run before a changed install plan is written. Uninstall plans
	// do not run it.
	Probe *Probe
}

type managerConfig struct {
	manager       ConfigManager
	agent         agent.Name
	scope         paths.Scope
	toolName      string
	invocation    string
	matcher       Matcher
	adoptUnmarked bool
	markerStyle   MarkerStyle
}

type desiredEntry struct {
	canonical Event
	event     string
	command   string
	id        string
}

type ownershipPolicy struct {
	toolName      string
	matcher       Matcher
	adoptUnmarked bool
	markerStyle   MarkerStyle
}

const (
	operationInstall   ChangeOperation = "install"
	operationUninstall ChangeOperation = "uninstall"
)

func (m ConfigManager) config() (managerConfig, error) {
	name := m.Agent
	markerStyle, err := resolveMarkerStyle(m.MarkerStyle, name)
	if err != nil {
		return managerConfig{}, err
	}
	if name != AgentClaude && name != AgentCodex {
		if name == AgentPi {
			return managerConfig{}, errors.New("pi has no declarative hook configuration; load a JavaScript extension instead")
		}
		return managerConfig{}, fmt.Errorf("unsupported hook configuration agent %q", m.Agent)
	}
	toolName := strings.TrimSpace(m.ToolName)
	if toolName == "" {
		return managerConfig{}, errors.New("hook configuration requires a tool name for ownership")
	}
	if strings.ContainsAny(toolName, "\r\n\x00") {
		return managerConfig{}, errors.New("hook configuration tool name contains a control character")
	}
	invocation := strings.TrimSpace(m.Invocation)
	if invocation != "" {
		if err := validateInvocation(invocation, toolName); err != nil {
			return managerConfig{}, err
		}
	} else if len(m.Hooks) > 0 {
		return managerConfig{}, errors.New("hook configuration requires an invocation for install plans")
	}
	matcher := m.Matcher
	if matcher == nil {
		matcher = MatchBasename(toolName)
	}
	if err := validateMatcher(matcher); err != nil {
		return managerConfig{}, fmt.Errorf("invalid hook matcher: %w", err)
	}
	scope := m.Scope
	if scope == "" {
		scope = paths.ScopeUser
	}
	return managerConfig{
		manager:       m,
		agent:         name,
		scope:         scope,
		toolName:      toolName,
		invocation:    invocation,
		matcher:       matcher,
		adoptUnmarked: m.AdoptUnmarked,
		markerStyle:   markerStyle,
	}, nil
}

func (c managerConfig) policy() ownershipPolicy {
	return ownershipPolicy{
		toolName:      c.toolName,
		matcher:       c.matcher,
		adoptUnmarked: c.adoptUnmarked,
		markerStyle:   c.markerStyle,
	}
}

func (c managerConfig) settingsPath() (string, error) {
	if value := strings.TrimSpace(c.manager.SettingsPath); value != "" {
		return value, nil
	}
	resolver := c.manager.Resolver
	var path string
	switch c.agent {
	case AgentClaude:
		path = resolver.ClaudeConfigPath(c.scope, c.manager.CWD)
	case AgentCodex:
		path = resolver.CodexHooksPath(c.scope, c.manager.CWD)
	default:
		return "", fmt.Errorf("unsupported hook configuration agent %q", c.agent)
	}
	if path == "" {
		return "", fmt.Errorf("agent %s does not support %s hook configuration scope", c.agent, c.scope)
	}
	return path, nil
}

func (c managerConfig) desired() ([]desiredEntry, []string, error) {
	entries := make([]desiredEntry, 0, len(c.manager.Hooks))
	order := make([]string, 0, len(c.manager.Hooks))
	usedIDs := map[string]bool{}
	for index, spec := range c.manager.Hooks {
		command := spec.Command
		if strings.TrimSpace(command) == "" {
			return nil, nil, fmt.Errorf("hook %d has an empty command", index)
		}
		agentEvent, ok := FromCanonical(c.agent, spec.Event)
		if !ok {
			return nil, nil, fmt.Errorf("event %q is not supported by %s", spec.Event, c.agent)
		}
		id := strings.TrimSpace(spec.ID)
		explicitID := id != ""
		if id == "" {
			id = defaultHookID(spec.Event, command)
		}
		if usedIDs[id] {
			if explicitID {
				return nil, nil, fmt.Errorf("duplicate hook entry id %q", id)
			}
			base := id
			for suffix := 2; ; suffix++ {
				id = fmt.Sprintf("%s-%d", base, suffix)
				if !usedIDs[id] {
					break
				}
			}
		}
		usedIDs[id] = true
		if !containsString(order, agentEvent) {
			order = append(order, agentEvent)
		}
		entries = append(entries, desiredEntry{
			canonical: spec.Event,
			event:     agentEvent,
			command:   command,
			id:        id,
		})
	}
	return entries, order, nil
}

func defaultHookID(event Event, command string) string {
	action := actionKey(command)
	if action == "" {
		action = "command"
	}
	return markerToken(string(event)) + "-" + markerToken(action)
}

func markerToken(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// PlanInstall reads and validates the prospective install without writing.
// The returned plan contains the real diff and entry summary for presentation
// by a caller. A non-nil plan may accompany an error so an unmarked-entry
// report can still be inspected when command-suffix style is selected; none
// style treats matcher matches as owned directly.
func (m ConfigManager) PlanInstall() (ChangePlan, error) {
	return m.plan(operationInstall)
}

// PlanUninstall reads and validates the prospective removal without writing.
func (m ConfigManager) PlanUninstall() (ChangePlan, error) {
	return m.plan(operationUninstall)
}

// Install is the convenience form of PlanInstall followed by Apply. It does
// not prompt; callers that need confirmation should call PlanInstall, display
// it, then Apply themselves.
func (m ConfigManager) Install(ctx context.Context, options ApplyOptions) (ChangePlan, error) {
	plan, err := m.PlanInstall()
	if err != nil {
		return plan, err
	}
	if err := m.Apply(ctx, plan, options); err != nil {
		return plan, err
	}
	return plan, nil
}

// Uninstall is the convenience form of PlanUninstall followed by Apply.
func (m ConfigManager) Uninstall(ctx context.Context, options ApplyOptions) (ChangePlan, error) {
	plan, err := m.PlanUninstall()
	if err != nil {
		return plan, err
	}
	if err := m.Apply(ctx, plan, options); err != nil {
		return plan, err
	}
	return plan, nil
}

// Apply writes a plan after re-reading and re-validating the target. If the
// file changed after planning, Apply refuses rather than overwriting a newer
// user edit. It runs the configured install probe immediately before the first
// write, unless SkipProbe is explicit.
func (m ConfigManager) Apply(ctx context.Context, plan ChangePlan, options ApplyOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c, err := m.config()
	if err != nil {
		return err
	}
	path, err := c.settingsPath()
	if err != nil {
		return err
	}
	if plan.path != path || plan.operation == "" || plan.agent != c.agent || plan.toolName != c.toolName || plan.invocation != c.invocation || plan.markerStyle != c.markerStyle {
		return errors.New("change plan belongs to a different configuration manager")
	}
	if !plan.HasChanges {
		return nil
	}
	currentExists := false
	if _, statErr := os.Stat(path); statErr == nil {
		currentExists = true
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat %s before apply: %w", path, statErr)
	}
	if currentExists != plan.beforeExist {
		return fmt.Errorf("refusing to apply %s: file existence changed after the plan was built", path)
	}
	current, err := loadSettings(path)
	if err != nil {
		return err
	}
	if !reflectSettingsEqual(current, plan.before) {
		return fmt.Errorf("refusing to apply %s: configuration changed after the plan was built", path)
	}
	if err := verifyChangeSafe(plan.before, plan.after, c.policy(), plan.operation == operationInstall); err != nil {
		return fmt.Errorf("refusing to write %s: %w", path, err)
	}
	if plan.operation == operationInstall && m.Probe != nil && !options.SkipProbe {
		if _, err := RunProbe(ctx, *m.Probe); err != nil {
			return err
		}
	}
	return saveSettings(path, plan.after, c.toolName)
}

// RunProbe executes a configured self-check directly and verifies its token.
// It is exported for callers that want to perform the check while rendering a
// plan, before asking for confirmation.
func RunProbe(ctx context.Context, probe Probe) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(probe.Command) == 0 || strings.TrimSpace(probe.Command[0]) == "" {
		return "", errors.New("self-check requires a non-empty command")
	}
	if strings.TrimSpace(probe.ExpectedToken) == "" {
		return "", errors.New("self-check requires an expected token")
	}
	timeout := probe.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	probeCommand := append([]string(nil), probe.Command...)
	run := probe.CommandRunner
	if run == nil {
		run = func(runCtx context.Context, argv []string) ([]byte, error) {
			// The token is a stdout protocol. Keep diagnostic stderr from
			// turning an otherwise successful probe into a false negative.
			return exec.CommandContext(runCtx, argv[0], argv[1:]...).Output()
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := run(checkCtx, probeCommand)
	label := strings.Join(probeCommand, " ")
	if checkErr := checkCtx.Err(); checkErr != nil {
		if errors.Is(checkErr, context.DeadlineExceeded) {
			return "", fmt.Errorf("self-check %s timed out after %s", label, timeout)
		}
		return "", fmt.Errorf("self-check %s canceled: %w", label, checkErr)
	}
	if err != nil {
		return "", fmt.Errorf("self-check %s failed: %w", label, err)
	}
	got := strings.TrimSpace(string(output))
	if got != strings.TrimSpace(probe.ExpectedToken) {
		return "", fmt.Errorf("self-check %s printed %q, want %q", label, got, strings.TrimSpace(probe.ExpectedToken))
	}
	return got, nil
}

func validateInvocation(invocation, toolName string) error {
	words, err := parseShellWords(invocation)
	if err != nil {
		return fmt.Errorf("parse hook invocation %q: %w", invocation, err)
	}
	if len(words) != 1 {
		return fmt.Errorf("hook invocation %q must contain only the executable argv[0]", invocation)
	}
	if commandBase(words[0].Text) != toolName {
		return fmt.Errorf("hook invocation %q does not name tool %q", invocation, toolName)
	}
	return nil
}

type shellWord struct {
	Text       string
	Start, End int
}

func parseShellWords(command string) ([]shellWord, error) {
	var words []shellWord
	for i := 0; i < len(command); {
		for i < len(command) && (command[i] == ' ' || command[i] == '\t' || command[i] == '\n' || command[i] == '\r') {
			i++
		}
		if i >= len(command) {
			break
		}
		start := i
		var word strings.Builder
		started := false
		for i < len(command) {
			switch command[i] {
			case ' ', '\t', '\n', '\r':
				if !started {
					i++
					continue
				}
				goto done
			case '\\':
				started = true
				i++
				if i >= len(command) {
					return nil, errors.New("trailing escape")
				}
				word.WriteByte(command[i])
				i++
			case '\'':
				started = true
				i++
				for i < len(command) && command[i] != '\'' {
					word.WriteByte(command[i])
					i++
				}
				if i >= len(command) {
					return nil, errors.New("unterminated single quote")
				}
				i++
			case '"':
				started = true
				i++
				for i < len(command) && command[i] != '"' {
					if command[i] == '\\' && i+1 < len(command) {
						i++
					}
					word.WriteByte(command[i])
					i++
				}
				if i >= len(command) {
					return nil, errors.New("unterminated double quote")
				}
				i++
			default:
				started = true
				word.WriteByte(command[i])
				i++
			}
		}
	done:
		if !started {
			continue
		}
		words = append(words, shellWord{Text: word.String(), Start: start, End: i})
	}
	return words, nil
}

func isShellAssignment(value string) bool {
	separator := strings.IndexByte(value, '=')
	if separator <= 0 {
		return false
	}
	name := value[:separator]
	for i := 0; i < len(name); i++ {
		c := name[i]
		if i == 0 {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_') {
				return false
			}
			continue
		}
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func commandExecutableIndex(words []shellWord) int {
	if len(words) == 0 {
		return -1
	}
	i := 0
	for i < len(words) && isShellAssignment(words[i].Text) {
		i++
	}
	if i >= len(words) || words[i].Text != "env" {
		return i
	}
	i++
	for i < len(words) {
		value := words[i].Text
		switch value {
		case "--":
			return i + 1
		case "-u", "--unset", "-C", "--chdir":
			i += 2
			continue
		}
		if strings.HasPrefix(value, "--unset=") || strings.HasPrefix(value, "--chdir=") || isShellAssignment(value) || strings.HasPrefix(value, "-") {
			i++
			continue
		}
		return i
	}
	return i
}

func commandBase(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "'\"")
	value = strings.TrimRight(value, "/\\")
	if index := strings.LastIndexAny(value, "/\\"); index >= 0 {
		value = value[index+1:]
	}
	return value
}

func splitArgv0(command string) (argv0, rest string) {
	command, _ = StripCommandSuffixMarker(command)
	trimmed := strings.TrimLeft(command, " \t\n\r")
	words, err := parseShellWords(trimmed)
	if err != nil || len(words) == 0 {
		return trimmed, ""
	}
	return trimmed[words[0].Start:words[0].End], trimmed[words[0].End:]
}

// actionKey identifies the subcommand portion of a command while ignoring
// flags. The executable may be wrapped in assignments or env; this is why it
// does not use strings.Fields.
func actionKey(command string) string {
	command, _ = StripCommandSuffixMarker(command)
	words, err := parseShellWords(command)
	if err != nil {
		return ""
	}
	index := commandExecutableIndex(words)
	if index < 0 || index >= len(words) {
		return ""
	}
	var parts []string
	for _, word := range words[index+1:] {
		if strings.HasPrefix(word.Text, "-") {
			break
		}
		parts = append(parts, word.Text)
	}
	return strings.Join(parts, " ")
}

func hooksSection(settings map[string]any, create bool) map[string]any {
	if raw, ok := settings["hooks"]; ok {
		if m, ok := raw.(map[string]any); ok {
			return m
		}
		return nil
	}
	if !create {
		return nil
	}
	m := map[string]any{}
	settings["hooks"] = m
	return m
}

func eventCommands(settings map[string]any, event string) []string {
	hooks := hooksSection(settings, false)
	if hooks == nil {
		return nil
	}
	groups, _ := hooks[event].([]any)
	var out []string
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
			if command, ok := hook["command"].(string); ok {
				out = append(out, command)
			}
		}
	}
	return out
}

func cloneSettings(settings map[string]any) (map[string]any, error) {
	body, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("copy settings: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("copy settings: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func cloneHookEntry(raw map[string]any) map[string]any {
	body, err := json.Marshal(raw)
	if err == nil {
		var out map[string]any
		if json.Unmarshal(body, &out) == nil && out != nil {
			return out
		}
	}
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

func matcherOwnsEntry(event string, raw map[string]any, command string, policy ownershipPolicy) bool {
	if matcherIsNil(policy.matcher) {
		return false
	}
	command, _ = StripCommandSuffixMarker(command)
	return policy.matcher.Match(HookEntry{Event: event, Command: command, Fields: cloneHookEntry(raw)})
}

func entryCommand(entry map[string]any) string {
	command, _ := entry["command"].(string)
	return command
}

// markerFor checks the command suffix first because it is the current
// authoritative marker. The legacy JSON fields are checked only when no valid
// suffix is present; they are read-only compatibility metadata and are never
// emitted by this package.
func markerFor(entry map[string]any, toolName string) (id string, marked bool, hasMarker bool) {
	if _, owner, suffixID, ok := ParseCommandSuffixMarker(entryCommand(entry)); ok {
		return suffixID, owner == toolName, true
	}
	return legacyMarkerFor(entry, toolName)
}

func classifyEntry(event string, raw map[string]any, policy ownershipPolicy) (owned, unmarked bool) {
	_, marked, hasMarker := markerFor(raw, policy.toolName)
	if hasMarker {
		return marked, false
	}
	if !matcherOwnsEntry(event, raw, entryCommand(raw), policy) {
		return false, false
	}
	if policy.markerStyle == MarkerStyleCommandSuffix {
		return false, true
	}
	return true, false
}

func collectUnmarked(settings map[string]any, policy ownershipPolicy) []UnmarkedEntry {
	hooks := hooksSection(settings, false)
	if hooks == nil {
		return nil
	}
	var out []UnmarkedEntry
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
				_, unmarked := classifyEntry(event, hook, policy)
				if unmarked {
					out = append(out, UnmarkedEntry{Event: event, Command: entryCommand(hook)})
				}
			}
		}
	}
	return out
}

func asyncOwnedEntries(settings map[string]any, policy ownershipPolicy) []UnmarkedEntry {
	hooks := hooksSection(settings, false)
	if hooks == nil {
		return nil
	}
	var out []UnmarkedEntry
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
				if (owned || unmarked) && hookBool(hook, "async") {
					out = append(out, UnmarkedEntry{Event: event, Command: entryCommand(hook)})
				}
			}
		}
	}
	return out
}

func hookBool(entry map[string]any, key string) bool {
	value, _ := entry[key].(bool)
	return value
}

func newHookEntry(entry desiredEntry, toolName string, markerStyle MarkerStyle) map[string]any {
	return map[string]any{
		"type":    "command",
		"command": commandForMarkerStyle(entry.command, toolName, entry.id, markerStyle),
	}
}

// stripOwnershipMarkers removes both current suffix metadata and legacy field
// metadata from an entry already classified as owned. Callers must not use it
// on foreign entries: removing a suffix changes their command.
func stripOwnershipMarkers(entry map[string]any) {
	if command, stripped := StripCommandSuffixMarker(entryCommand(entry)); stripped {
		entry["command"] = command
	}
	delete(entry, legacyMarkerOwnerField)
	for key := range entry {
		if looksLikeLegacyMarkerIDField(key) {
			delete(entry, key)
		}
	}
}

func mergeDesired(settings map[string]any, desired []desiredEntry, policy ownershipPolicy) error {
	hooks := hooksSection(settings, true)
	if hooks == nil {
		return errors.New(`the settings file has a "hooks" key that is not an object`)
	}
	byEvent := map[string][]any{}
	order := make([]string, 0, len(desired))
	for _, entry := range desired {
		if !containsString(order, entry.event) {
			order = append(order, entry.event)
		}
		byEvent[entry.event] = append(byEvent[entry.event], newHookEntry(entry, policy.toolName, policy.markerStyle))
	}
	for _, event := range order {
		inner := byEvent[event]
		groups, exists := hooks[event]
		if exists {
			parsed, ok := groups.([]any)
			if !ok {
				return fmt.Errorf("event %q is not an array of hook groups", event)
			}
			hooks[event] = append(parsed, map[string]any{"hooks": inner})
			continue
		}
		hooks[event] = []any{map[string]any{"hooks": inner}}
	}
	return nil
}

func removeOwnedEntries(settings map[string]any, policy ownershipPolicy) []EntryChange {
	hooks := hooksSection(settings, false)
	if hooks == nil {
		return nil
	}
	var removed []EntryChange
	for _, event := range sortedKeys(hooks) {
		rawGroups, ok := hooks[event].([]any)
		if !ok {
			continue
		}
		keptGroups := make([]any, 0, len(rawGroups))
		for _, rawGroup := range rawGroups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				keptGroups = append(keptGroups, rawGroup)
				continue
			}
			inner, ok := group["hooks"].([]any)
			if !ok {
				keptGroups = append(keptGroups, rawGroup)
				continue
			}
			keptInner := make([]any, 0, len(inner))
			removedFromGroup := false
			for _, rawHook := range inner {
				hook, ok := rawHook.(map[string]any)
				if !ok {
					keptInner = append(keptInner, rawHook)
					continue
				}
				owned, unmarked := classifyEntry(event, hook, policy)
				if owned || (unmarked && policy.adoptUnmarked) {
					removedFromGroup = true
					removed = append(removed, EntryChange{Event: event, Command: entryCommand(hook), ID: markerIDForChange(hook, policy.toolName)})
					continue
				}
				keptInner = append(keptInner, rawHook)
			}
			if len(keptInner) == 0 && removedFromGroup {
				continue
			}
			group["hooks"] = keptInner
			keptGroups = append(keptGroups, group)
		}
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return removed
}

func markerIDForChange(entry map[string]any, toolName string) string {
	id, marked, _ := markerFor(entry, toolName)
	if marked {
		return id
	}
	return ""
}

func convergeEntries(settings map[string]any, policy ownershipPolicy, desired []desiredEntry) (added, removed []EntryChange, err error) {
	hooks := hooksSection(settings, true)
	if hooks == nil {
		return nil, nil, errors.New(`the settings file has a "hooks" key that is not an object`)
	}
	wantedEvents := map[string]bool{}
	for _, entry := range desired {
		wantedEvents[entry.event] = true
	}
	type oldEntry struct {
		event   string
		command string
		id      string
		action  string
		value   map[string]any
	}
	anchors := map[string]map[string]any{}
	anchorPositions := map[string]int{}
	byID := map[string]oldEntry{}
	legacyByAction := map[string][]oldEntry{}
	for _, event := range sortedKeys(hooks) {
		rawGroups, ok := hooks[event].([]any)
		if !ok {
			continue
		}
		keptGroups := make([]any, 0, len(rawGroups))
		for _, rawGroup := range rawGroups {
			group, ok := rawGroup.(map[string]any)
			if !ok {
				keptGroups = append(keptGroups, rawGroup)
				continue
			}
			inner, ok := group["hooks"].([]any)
			if !ok {
				keptGroups = append(keptGroups, rawGroup)
				continue
			}
			keptInner := make([]any, 0, len(inner))
			anchorForGroup := false
			ownedInGroup := false
			for _, rawHook := range inner {
				hook, ok := rawHook.(map[string]any)
				if !ok {
					keptInner = append(keptInner, rawHook)
					continue
				}
				owned, unmarked := classifyEntry(event, hook, policy)
				if !owned && !(unmarked && policy.adoptUnmarked) {
					keptInner = append(keptInner, rawHook)
					continue
				}
				ownedInGroup = true
				command := entryCommand(hook)
				old := oldEntry{event: event, command: command, action: actionKey(command), value: cloneHookEntry(hook)}
				if id, isMarked, _ := markerFor(hook, policy.toolName); isMarked {
					old.id = id
					if _, exists := byID[id]; !exists {
						byID[id] = old
					}
				} else {
					key := event + "\x00" + old.action
					legacyByAction[key] = append(legacyByAction[key], old)
				}
				removed = append(removed, EntryChange{Event: event, Command: command, ID: old.id})
				if anchors[event] == nil {
					anchors[event] = group
					anchorPositions[event] = len(keptInner)
					anchorForGroup = true
				}
			}
			if len(keptInner) == 0 && ownedInGroup && !(anchorForGroup && wantedEvents[event]) {
				continue
			}
			group["hooks"] = keptInner
			keptGroups = append(keptGroups, group)
		}
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	usedIDs := map[string]bool{}
	usedActions := map[string]int{}
	for _, entry := range desired {
		var old *oldEntry
		if candidate, ok := byID[entry.id]; ok && !usedIDs[entry.id] {
			copy := candidate
			old = &copy
			usedIDs[entry.id] = true
		}
		if old == nil {
			key := entry.event + "\x00" + actionKey(entry.command)
			index := usedActions[key]
			if index < len(legacyByAction[key]) {
				candidate := legacyByAction[key][index]
				usedActions[key] = index + 1
				copy := candidate
				old = &copy
			}
		}
		var hook map[string]any
		command := entry.command
		if old != nil {
			hook = cloneHookEntry(old.value)
			// Remove any old suffix or JSON-field metadata before applying
			// the style selected for this install. The declared command is
			// authoritative; wrapper text and hand-edited flags from the old
			// command are not preserved.
			stripOwnershipMarkers(hook)
			command = commandForMarkerStyle(entry.command, policy.toolName, entry.id, policy.markerStyle)
			hook["command"] = command
		} else {
			hook = newHookEntry(entry, policy.toolName, policy.markerStyle)
			command = entryCommand(hook)
		}
		if anchor := anchors[entry.event]; anchor != nil {
			inner, _ := anchor["hooks"].([]any)
			position := anchorPositions[entry.event]
			if position < 0 || position > len(inner) {
				position = len(inner)
			}
			insert := []any{hook}
			inner = append(inner[:position], append(insert, inner[position:]...)...)
			anchor["hooks"] = inner
			anchorPositions[entry.event] = position + 1
		} else {
			rawGroups, exists := hooks[entry.event]
			groups := []any(nil)
			if exists {
				var ok bool
				groups, ok = rawGroups.([]any)
				if !ok {
					return nil, nil, fmt.Errorf("event %q is not an array of hook groups", entry.event)
				}
			}
			group := map[string]any{"hooks": []any{hook}}
			hooks[entry.event] = append(groups, group)
		}
		added = append(added, EntryChange{Event: entry.event, Command: command, ID: entry.id})
	}
	return added, removed, nil
}

func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".crossagent-*")
	if err != nil {
		return fmt.Errorf("create a temporary file next to %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set the mode on %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("flush %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("move %s into place at %s: %w", tmpName, path, err)
	}
	return nil
}

const defaultSettingsMode = os.FileMode(0o644)

func settingsMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		if mode := info.Mode().Perm(); mode != 0 {
			return mode
		}
	}
	return defaultSettingsMode
}

func backupPath(path, toolName string) string {
	name := markerToken(toolName)
	if name == "" {
		name = "crossagent"
	}
	return path + "." + name + ".bak"
}

func backupSettings(path string, toolName ...string) error {
	name := "crossagent"
	if len(toolName) > 0 && strings.TrimSpace(toolName[0]) != "" {
		name = toolName[0]
	}
	backup := backupPath(path, name)
	if _, err := os.Stat(backup); err == nil {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s for backup: %w", path, err)
	}
	return writeFileAtomic(backup, body, settingsMode(path))
}

func saveSettings(path string, settings map[string]any, toolName ...string) error {
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := backupSettings(path, toolName...); err != nil {
		return err
	}
	return writeFileAtomic(path, append(body, '\n'), settingsMode(path))
}

func loadSettings(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	settings, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parse %s: top-level JSON value is not an object", path)
	}
	return settings, nil
}

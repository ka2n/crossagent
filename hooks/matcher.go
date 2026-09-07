package hooks

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Matcher recognizes a hook entry when no ownership marker is available.
// Match receives the entry's clean command, with any command-suffix marker
// removed. Fields remains the complete hook-entry object so a MatchFunc can
// inspect vendor-specific fields as well as the command.
type Matcher interface {
	Match(HookEntry) bool
}

// MatchBasename returns a matcher that compares the basename of the command's
// first shell word with tool. It deliberately does not unwrap assignments or
// env. This is the default matcher; wrapper recognition must be requested
// explicitly with MatchEnvWrapped.
func MatchBasename(tool string) Matcher {
	return basenameMatcher{tool: strings.TrimSpace(tool)}
}

// MatchPattern returns a matcher that applies expr to the clean command. The
// expression is compiled eagerly. An unanchored expression is a footgun: it
// can recognize an unrelated command merely because the text appears later in
// the command. Prefer an anchored expression such as
// `^/opt/mytool(?:\s|$)` when matching an executable and its arguments.
//
// When expr is invalid, the returned matcher is invalid as well as the error
// being returned. If the error is ignored and the matcher is placed in a
// ConfigManager, planning fails instead of silently matching nothing.
func MatchPattern(expr string) (Matcher, error) {
	compiled, err := regexp.Compile(expr)
	if err != nil {
		return patternMatcher{expression: expr, err: err}, err
	}
	return patternMatcher{expression: expr, compiled: compiled}, nil
}

// MatchEnvWrapped decorates m so it recognizes the command after leading
// shell VAR=value assignments and a leading env command with its common flags
// are removed. The unwrapping behavior is intentionally opt-in because a
// command such as `env X=1 tool ...` makes tool an argument of env; adopting it
// by default could clobber a hand-tuned wrapper when convergence writes the
// declared HookSpec.Command verbatim. If a wrapped entry is recognized, the
// wrapper is not preserved during convergence; declare it in HookSpec.Command
// when it is desired.
func MatchEnvWrapped(m Matcher) Matcher {
	return envWrappedMatcher{matcher: m}
}

// MatchAny returns a matcher that succeeds when any child matcher succeeds.
// It is the logical OR operation; with no children it always returns false.
func MatchAny(ms ...Matcher) Matcher {
	return anyMatcher{matchers: append([]Matcher(nil), ms...)}
}

// MatchAll returns a matcher that succeeds when every child matcher succeeds.
// It is the logical AND operation; with no children it always returns true.
func MatchAll(ms ...Matcher) Matcher {
	return allMatcher{matchers: append([]Matcher(nil), ms...)}
}

// MatchFunc adapts a function into a Matcher. It is the raw escape hatch for
// ownership rules that need to inspect HookEntry.Fields or implement logic not
// covered by the built-in vocabulary.
func MatchFunc(f func(HookEntry) bool) Matcher {
	return funcMatcher{fn: f}
}

type basenameMatcher struct {
	tool string
}

func (m basenameMatcher) Match(entry HookEntry) bool {
	entry = cleanMatcherEntry(entry)
	words, err := parseShellWords(entry.Command)
	if err != nil || len(words) == 0 {
		return false
	}
	return commandBase(words[0].Text) == m.tool
}

type patternMatcher struct {
	expression string
	compiled   *regexp.Regexp
	err        error
}

func (m patternMatcher) Match(entry HookEntry) bool {
	if m.compiled == nil {
		return false
	}
	return m.compiled.MatchString(cleanMatcherEntry(entry).Command)
}

func (m patternMatcher) validateMatcher() error {
	if m.err != nil {
		return fmt.Errorf("pattern %q: %w", m.expression, m.err)
	}
	if m.compiled == nil {
		return fmt.Errorf("pattern %q was not compiled", m.expression)
	}
	return nil
}

type envWrappedMatcher struct {
	matcher Matcher
}

func (m envWrappedMatcher) Match(entry HookEntry) bool {
	if matcherIsNil(m.matcher) {
		return false
	}
	entry = cleanMatcherEntry(entry)
	words, err := parseShellWords(entry.Command)
	if err != nil || len(words) == 0 {
		return false
	}
	index := commandExecutableIndex(words)
	if index < 0 || index >= len(words) {
		return false
	}
	entry.Command = entry.Command[words[index].Start:]
	return m.matcher.Match(entry)
}

func (m envWrappedMatcher) validateMatcher() error {
	if err := validateMatcher(m.matcher); err != nil {
		return fmt.Errorf("env-wrapped delegate: %w", err)
	}
	return nil
}

type anyMatcher struct {
	matchers []Matcher
}

func (m anyMatcher) Match(entry HookEntry) bool {
	entry = cleanMatcherEntry(entry)
	for _, matcher := range m.matchers {
		if !matcherIsNil(matcher) && matcher.Match(entry) {
			return true
		}
	}
	return false
}

func (m anyMatcher) validateMatcher() error {
	return validateMatcherList("any", m.matchers)
}

type allMatcher struct {
	matchers []Matcher
}

func (m allMatcher) Match(entry HookEntry) bool {
	entry = cleanMatcherEntry(entry)
	for _, matcher := range m.matchers {
		if matcherIsNil(matcher) || !matcher.Match(entry) {
			return false
		}
	}
	return true
}

func (m allMatcher) validateMatcher() error {
	return validateMatcherList("all", m.matchers)
}

type funcMatcher struct {
	fn func(HookEntry) bool
}

func (m funcMatcher) Match(entry HookEntry) bool {
	if m.fn == nil {
		return false
	}
	entry = cleanMatcherEntry(entry)
	entry.Fields = cloneMatcherFields(entry.Fields)
	return m.fn(entry)
}

func (m funcMatcher) validateMatcher() error {
	if m.fn == nil {
		return fmt.Errorf("matcher function is nil")
	}
	return nil
}

// matcherValidator is implemented by matchers constructed by this package so
// invalid children and ignored MatchPattern errors can be reported while a
// plan is built. Third-party Matcher implementations need only implement the
// public Match method.
type matcherValidator interface {
	validateMatcher() error
}

func validateMatcher(matcher Matcher) error {
	if matcherIsNil(matcher) {
		return fmt.Errorf("matcher is nil")
	}
	if validator, ok := matcher.(matcherValidator); ok {
		return validator.validateMatcher()
	}
	return nil
}

func validateMatcherList(kind string, matchers []Matcher) error {
	for index, matcher := range matchers {
		if err := validateMatcher(matcher); err != nil {
			return fmt.Errorf("%s matcher %d: %w", kind, index, err)
		}
	}
	return nil
}

func matcherIsNil(matcher Matcher) bool {
	if matcher == nil {
		return true
	}
	value := reflect.ValueOf(matcher)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func cleanMatcherEntry(entry HookEntry) HookEntry {
	entry.Command, _ = StripCommandSuffixMarker(entry.Command)
	return entry
}

func cloneMatcherFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	return cloneHookEntry(fields)
}

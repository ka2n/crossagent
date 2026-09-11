// Package crossagent provides reusable building blocks for tooling that spans
// multiple coding-agent CLIs such as Claude Code, Codex, and pi.
//
// The agent subpackage holds the canonical agent-name vocabulary as a typed
// Name; every other package takes that type rather than a bare string, and
// this package re-exports it as crossagent.Name with the three constants.
//
// The library covers agent detection, session-location/path facts, a
// normalized hook event and payload vocabulary, plan-first hook configuration
// management, and lossless source-line transcript reading with structural
// vendor parsing. Path resolution and hook facts remain pure; the hooks
// package's scoped manager is the explicit mutation layer and never injects
// live messages. Process liveness and cross-namespace PID resolution
// are deliberately out of scope: they are Linux-only, depend on undocumented
// /proc details, and are impossible from inside a sandbox without cooperation
// from outside it.
package crossagent

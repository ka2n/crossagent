// Package crossagent provides reusable building blocks for tooling that spans
// multiple coding-agent CLIs such as Claude Code, Codex, and pi.
//
// The library covers agent detection, session-location/path facts, a
// normalized hook event and payload vocabulary, and plan-first hook
// configuration management. Path resolution and hook facts remain pure; the
// hooks package's scoped manager is the explicit mutation layer and never
// injects live messages. Process liveness and cross-namespace PID resolution
// are deliberately out of scope: they are Linux-only, depend on undocumented
// /proc details, and are impossible from inside a sandbox without cooperation
// from outside it.
package crossagent

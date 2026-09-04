// Package crossagent provides reusable building blocks for tooling that spans
// multiple coding-agent CLIs such as Claude Code, Codex, and pi.
//
// The library covers agent detection, pure session/path facts, and a normalized
// hook event and payload vocabulary. Path resolution and hook facts do not
// perform filesystem discovery or mutate configuration; installation and
// configuration-management operations are the next layer built on these
// facts. Process liveness and cross-namespace PID resolution are deliberately
// out of scope: they are Linux-only, depend on undocumented /proc details, and
// are impossible from inside a sandbox without cooperation from outside it.
package crossagent

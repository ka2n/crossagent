// Package crossagent provides reusable building blocks for tooling that spans
// multiple coding-agent CLIs such as Claude Code, Codex, and pi.
//
// The eventual library covers four capabilities: detecting installed agents,
// discovering where their sessions live, injecting messages into running
// sessions, and installing or managing hook configuration. Detection is
// available today; session-location discovery is the next addition. Process
// liveness and cross-namespace PID resolution are deliberately out of scope:
// they are Linux-only, depend on undocumented /proc details, and are
// impossible from inside a sandbox without cooperation from outside it.
package crossagent

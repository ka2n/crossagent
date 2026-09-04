// Package crossagent provides reusable building blocks for tooling that spans
// multiple coding-agent CLIs.
//
// The eventual library covers four capabilities: detecting installed agents,
// discovering their sessions, injecting messages into running sessions, and
// installing or managing their hook configuration. This first pass ships
// detection and process identity across Linux PID namespaces; session
// discovery, message injection, and hook management are intentionally left for
// later passes.
package crossagent

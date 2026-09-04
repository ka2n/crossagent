// Package hostpid resolves process identities collected inside a PID namespace
// to host-visible PIDs and provides the Linux pidfd process monitor.
//
// Resolution is directional:
//
//   - In the same PID namespace, no translation is needed.
//   - A host consumer can resolve an agent in a nested namespace (for example
//     Docker, jai, or systemd-nspawn) by scanning visible processes and using
//     their NSpid translation table. This is the case this package exists for.
//   - A consumer inside a sandbox cannot resolve an agent outside that sandbox:
//     the outer process is not visible through its /proc, so resolution is
//     impossible and fails closed.
//   - A consumer in container A cannot resolve an agent in sibling container B
//     for the same reason.
//
// The NSpid field is available since Linux 4.1. Reading another user's
// /proc/<pid>/status may be denied by procfs permissions, in which case the
// resolver treats the translation as unavailable. CanResolve returns
// ErrUnsupportedDirection when a caller should try an environment-specific
// backend instead of interpreting Resolve's false as an ordinary not-found.
// Non-Linux builds return an explicit unsupported error from Linux-only
// operations.
//
// Recovering the impossible direction is intentionally an additive,
// environment-owned concern. A mounted host procfs works today with
// configuration alone: pass procfs.New("/host/proc") to NewResolver; it wires
// all five public reader fields consistently, and callers may replace any one
// reader for a specialized backend. A Docker or podman socket can provide the
// host PID directly with `docker inspect --format {{.State.Pid}}`; Kubernetes
// hostPID: true removes the translation problem entirely; and a host-side
// helper reached over a bind-mounted socket can perform the lookup (the
// architecture used by the source way-island project: a host daemon and hooks
// inside the namespace). Every option requires cooperation from outside the
// sandbox. With no read path and no channel to the outside, the information is
// not present inside the sandbox; that is not an implementation gap.
package hostpid

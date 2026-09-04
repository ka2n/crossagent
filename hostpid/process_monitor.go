package hostpid

import "errors"

// ProcessMonitor observes a process and invokes its callback once when the
// process exits. Close is idempotent.
type ProcessMonitor interface {
	Close() error
}

// ErrProcessMonitorUnsupported is returned where Linux pidfds are not
// available.
var ErrProcessMonitorUnsupported = errors.New("pidfd process monitor is unsupported on this platform")

// NewProcessMonitor resolves identity with the host /proc resolver and starts
// an exit monitor. The callback is not invoked after an intentional Close.
func NewProcessMonitor(identity ProcessIdentity, onExit func()) (ProcessMonitor, error) {
	return NewProcessMonitorWithResolver(DefaultResolver(), identity, onExit)
}

// NewProcessMonitorWithResolver is the injectable form of NewProcessMonitor.
func NewProcessMonitorWithResolver(r Resolver, identity ProcessIdentity, onExit func()) (ProcessMonitor, error) {
	return newProcessMonitor(r, identity, onExit)
}

// NewMonitor is a concise alias for NewProcessMonitor.
func NewMonitor(identity ProcessIdentity, onExit func()) (ProcessMonitor, error) {
	return NewProcessMonitor(identity, onExit)
}

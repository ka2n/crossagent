//go:build !linux

package hostpid

func newProcessMonitor(_ Resolver, _ ProcessIdentity, _ func()) (ProcessMonitor, error) {
	return nil, ErrProcessMonitorUnsupported
}

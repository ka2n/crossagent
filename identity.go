package crossagent

import "github.com/ka2n/crossagent/hostpid"

// ProcessIdentity is re-exported for callers that use the root package for
// detection and process identity together. The implementation lives in the
// hostpid package so it can be used without depending on the detector.
type ProcessIdentity = hostpid.ProcessIdentity

// CollectProcessIdentity gathers the PID, PID-namespace inode, and start-time
// ticks for pid from the namespace in which this code is running. Hook
// processes should call this while they can still see the agent's namespace.
func CollectProcessIdentity(pid int) (ProcessIdentity, error) {
	return hostpid.Collect(pid)
}

// Package hostpid resolves process identities collected inside a PID
// namespace to host-visible PIDs and provides the Linux process monitor used
// by later session-management code.
package hostpid

import (
	"errors"
	"fmt"

	"github.com/ka2n/crossagent/procfs"
)

// ProcessIdentity is the small, transportable identity a hook must collect
// while it is inside the agent's PID namespace. PID alone is not sufficient:
// NamespaceInode identifies the namespace and StartTimeTicks detects PID
// reuse. InJail records an independently known container/jail boundary.
type ProcessIdentity struct {
	PID            int    `json:"pid"`
	NamespaceInode uint64 `json:"namespace_inode"`
	StartTimeTicks uint64 `json:"start_time_ticks"`
	InJail         bool   `json:"in_jail"`
}

// Resolver contains every host-side reader used by Resolve. Keeping these as
// function fields makes the namespace algorithm deterministic and testable
// without creating processes or depending on /proc in unit tests.
type Resolver struct {
	ReadCurrentPIDNSInode func() (uint64, error)
	ReadPIDNamespaceInode func(int) (uint64, error)
	ReadNamespacedPIDs    func(int) ([]int, error)
	ReadStartTimeTicks    func(int) (uint64, error)
	ListPIDs              func() ([]int, error)
}

// HostPIDResolver is a compatibility alias for the descriptive original
// name used by the source project.
type HostPIDResolver = Resolver

// ErrPIDMissing reports an identity without a usable PID.
var ErrPIDMissing = errors.New("process identity has no pid")

// ErrHostPIDNotFound reports that a host PID could not be established safely.
var ErrHostPIDNotFound = errors.New("host pid could not be resolved")

// NewResolver builds an injected resolver backed by fs.
func NewResolver(fs procfs.ProcFS) Resolver {
	return Resolver{
		ReadCurrentPIDNSInode: fs.ReadCurrentPIDNamespaceInode,
		ReadPIDNamespaceInode: fs.ReadPIDNamespaceInode,
		ReadNamespacedPIDs:    fs.ReadNamespacedPIDs,
		ReadStartTimeTicks:    fs.ReadStartTimeTicks,
		ListPIDs:              fs.ListPIDs,
	}
}

// DefaultResolver builds a resolver backed by the host /proc.
func DefaultResolver() Resolver {
	return NewResolver(procfs.Default())
}

// Collect gathers all three process-identity facts for pid from the current
// PID namespace. A hook should call this before it sends its event because a
// host-side observer cannot reconstruct the namespace-local PID afterwards.
// InJail is left false; callers that know they are running in a jail can set
// it on the returned value.
func Collect(pid int) (ProcessIdentity, error) {
	return CollectFrom(procfs.Default(), pid)
}

// CollectFrom is Collect with an injectable proc filesystem.
func CollectFrom(fs procfs.ProcFS, pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("%w: %d", ErrPIDMissing, pid)
	}

	namespaceInode, err := fs.ReadPIDNamespaceInode(pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("collect namespace inode for pid %d: %w", pid, err)
	}
	startTimeTicks, err := fs.ReadStartTimeTicks(pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("collect start time for pid %d: %w", pid, err)
	}
	return ProcessIdentity{
		PID:            pid,
		NamespaceInode: namespaceInode,
		StartTimeTicks: startTimeTicks,
	}, nil
}

// Resolve maps identity to a host PID. It first searches the namespace when
// the identity looks namespaced (jail, a small namespace-local PID, or a
// namespace inode different from the resolver's own). Otherwise it validates
// the raw PID first and then falls back to a namespace search.
func (r Resolver) Resolve(identity ProcessIdentity) (hostPID int, ok bool) {
	if identity.PID <= 0 {
		return 0, false
	}

	shouldTryNamespaceFirst := r.LooksNamespaced(identity)
	if shouldTryNamespaceFirst {
		if hostPID, ok := r.findByNamespace(identity); ok {
			return hostPID, true
		}
		// A namespaced PID must not be accepted merely because the host
		// happens to have the same number. Accept the raw number only when
		// the available identity facts actually validate it.
		if r.matchesNamespacedRawPID(identity.PID, identity) {
			return identity.PID, true
		}
		return r.findByNamespace(identity)
	}

	if r.matches(identity.PID, identity) {
		return identity.PID, true
	}

	return r.findByNamespace(identity)
}

// LooksNamespaced applies the ordering heuristic's namespace test. It is
// useful to callers that need the same fail-closed policy as the process
// monitor.
func (r Resolver) LooksNamespaced(identity ProcessIdentity) bool {
	if identity.InJail || identity.PID < 100 {
		return true
	}
	if r.ReadCurrentPIDNSInode == nil || identity.NamespaceInode == 0 {
		return false
	}
	currentNamespace, err := r.ReadCurrentPIDNSInode()
	return err == nil && currentNamespace > 0 && currentNamespace != identity.NamespaceInode
}

// ResolveForMonitoring resolves identity for a liveness check or pidfd. It
// preserves the fail-closed rule: when a namespaced identity cannot be
// resolved, it returns an error instead of probing the numeric PID in the host
// namespace, where that number may belong to an unrelated process.
func (r Resolver) ResolveForMonitoring(identity ProcessIdentity) (int, error) {
	if identity.PID <= 0 {
		return 0, fmt.Errorf("%w: %d", ErrPIDMissing, identity.PID)
	}
	if hostPID, ok := r.Resolve(identity); ok {
		return hostPID, nil
	}
	if r.LooksNamespaced(identity) {
		return 0, fmt.Errorf("%w: pid=%d", ErrHostPIDNotFound, identity.PID)
	}
	return identity.PID, nil
}

// ResolveMonitorPID is an alias for ResolveForMonitoring.
func (r Resolver) ResolveMonitorPID(identity ProcessIdentity) (int, error) {
	return r.ResolveForMonitoring(identity)
}

func (r Resolver) matchesNamespacedRawPID(pid int, identity ProcessIdentity) bool {
	// Namespace membership is the minimum proof that a raw host PID can
	// represent a namespace-local identity. If a recorded start time exists,
	// it must be checked too; otherwise PID reuse would remain possible.
	if identity.NamespaceInode == 0 || r.ReadPIDNamespaceInode == nil {
		return false
	}
	if identity.StartTimeTicks > 0 && r.ReadStartTimeTicks == nil {
		return false
	}
	return r.matches(pid, identity)
}

func (r Resolver) matches(pid int, identity ProcessIdentity) bool {
	if pid <= 0 {
		return false
	}

	if r.ReadStartTimeTicks != nil && identity.StartTimeTicks > 0 {
		startTimeTicks, err := r.ReadStartTimeTicks(pid)
		if err != nil || startTimeTicks != identity.StartTimeTicks {
			return false
		}
	}

	if r.ReadPIDNamespaceInode != nil && identity.NamespaceInode > 0 {
		namespaceInode, err := r.ReadPIDNamespaceInode(pid)
		if err != nil || namespaceInode != identity.NamespaceInode {
			return false
		}
	}

	return true
}

func (r Resolver) findByNamespace(identity ProcessIdentity) (int, bool) {
	if identity.PID <= 0 || identity.NamespaceInode == 0 {
		return 0, false
	}
	if r.ListPIDs == nil || r.ReadPIDNamespaceInode == nil || r.ReadNamespacedPIDs == nil {
		return 0, false
	}

	pids, err := r.ListPIDs()
	if err != nil {
		return 0, false
	}

	candidates := make([]int, 0, 1)
	for _, pid := range pids {
		namespaceInode, err := r.ReadPIDNamespaceInode(pid)
		if err != nil || namespaceInode != identity.NamespaceInode {
			continue
		}

		namespacedPIDs, err := r.ReadNamespacedPIDs(pid)
		if err != nil || !containsPID(namespacedPIDs, identity.PID) {
			continue
		}

		if r.ReadStartTimeTicks != nil && identity.StartTimeTicks > 0 {
			startTimeTicks, err := r.ReadStartTimeTicks(pid)
			if err != nil || startTimeTicks != identity.StartTimeTicks {
				continue
			}
		}
		candidates = append(candidates, pid)
	}

	if len(candidates) == 1 {
		return candidates[0], true
	}
	// With no recorded start time there is no way to distinguish multiple
	// matching host processes. Preserve the source algorithm's first-match
	// fallback for this explicitly degraded identity; a recorded start time
	// never accepts an ambiguous result.
	if len(candidates) > 1 && identity.StartTimeTicks == 0 {
		return candidates[0], true
	}
	return 0, false
}

func containsPID(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Resolve uses the default /proc-backed resolver.
func Resolve(identity ProcessIdentity) (int, bool) {
	return DefaultResolver().Resolve(identity)
}

// ResolveForMonitoring uses the default /proc-backed resolver and fail-closed
// semantics.
func ResolveForMonitoring(identity ProcessIdentity) (int, error) {
	return DefaultResolver().ResolveForMonitoring(identity)
}

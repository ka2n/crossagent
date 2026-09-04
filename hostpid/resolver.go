package hostpid

import (
	"errors"
	"fmt"

	"github.com/ka2n/crossagent/procfs"
)

// SuspiciousPIDThreshold is the namespace-local PID below which an identity
// is treated as probably namespaced when its recorded namespace inode is
// unavailable. It is only a heuristic; a recorded namespace inode always
// takes precedence.
const SuspiciousPIDThreshold = 100

// ProcessIdentity is the small, transportable identity a hook must collect
// while it is inside the agent's PID namespace. PID alone is not sufficient:
// NamespaceInode identifies the namespace and StartTimeTicks detects PID
// reuse. AssumeNamespaced is a neutral, explicit override for callers that
// know the process is namespaced even when the inode could not be collected.
type ProcessIdentity struct {
	PID              int    `json:"pid"`
	NamespaceInode   uint64 `json:"namespace_inode"`
	StartTimeTicks   uint64 `json:"start_time_ticks"`
	AssumeNamespaced bool   `json:"assume_namespaced"`
}

// Resolver contains every host-side reader used by Resolve. Keeping these as
// public function fields makes the namespace algorithm deterministic and
// testable without creating processes or depending on /proc in unit tests. An
// environment backend may replace any one reader without replacing the rest.
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

// ErrUnsupportedDirection reports that the consumer cannot inspect the
// namespace containing the target process. This is different from a
// resolvable namespace in which the target process simply is not present.
var ErrUnsupportedDirection = errors.New("PID namespace direction is unsupported from this consumer")

// ErrConsumerNamespaceUnknown reports that the consumer's own PID namespace
// could not be inspected, so an authoritative inode comparison is impossible.
var ErrConsumerNamespaceUnknown = errors.New("consumer PID namespace is unknown")

// NewResolver builds an injected resolver backed by fs. A monitoring
// container with the host procfs mounted at /host/proc can use
// NewResolver(procfs.New("/host/proc")) without a library-specific backend;
// all five readers then use that same root.
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
// It does not inspect any environment variable or sandbox-specific marker.
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

// NeedsTranslation derives whether identity needs an NSpid-based translation.
// A recorded namespace inode is authoritative: it is compared with the
// consumer's own inode. Only an identity with no recorded inode uses the
// low-PID heuristic. AssumeNamespaced explicitly forces the namespaced path.
func (r Resolver) NeedsTranslation(identity ProcessIdentity) (bool, error) {
	if identity.PID <= 0 {
		return false, fmt.Errorf("%w: %d", ErrPIDMissing, identity.PID)
	}
	if identity.AssumeNamespaced {
		return true, nil
	}
	if identity.NamespaceInode == 0 {
		return identity.PID < SuspiciousPIDThreshold, nil
	}
	if r.ReadCurrentPIDNSInode == nil {
		return false, fmt.Errorf("%w: current namespace reader is not configured", ErrConsumerNamespaceUnknown)
	}

	currentNamespace, err := r.ReadCurrentPIDNSInode()
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrConsumerNamespaceUnknown, err)
	}
	if currentNamespace == 0 {
		return false, fmt.Errorf("%w: current namespace inode is zero", ErrConsumerNamespaceUnknown)
	}
	return identity.NamespaceInode != currentNamespace, nil
}

// LooksNamespaced is a conservative, error-free form of NeedsTranslation.
// Unknown consumer namespace information returns true so callers do not
// accidentally fall back to probing a numeric host PID.
func (r Resolver) LooksNamespaced(identity ProcessIdentity) bool {
	needsTranslation, err := r.NeedsTranslation(identity)
	return err != nil || needsTranslation
}

// Resolve maps identity to a host PID. It first searches the namespace when
// the identity needs translation (an explicit override, a namespace inode
// mismatch, or the low-PID fallback). Otherwise it validates the raw PID
// directly and only then falls back to a namespace search.
func (r Resolver) Resolve(identity ProcessIdentity) (hostPID int, ok bool) {
	needsTranslation, err := r.NeedsTranslation(identity)
	if err != nil {
		return 0, false
	}
	return r.resolveWithDecision(identity, needsTranslation)
}

func (r Resolver) resolveWithDecision(identity ProcessIdentity, needsTranslation bool) (hostPID int, ok bool) {
	if needsTranslation {
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

// ResolveForMonitoring resolves identity for a liveness check or pidfd. It
// preserves the fail-closed rule: when a namespaced identity cannot be
// resolved, it returns an error instead of probing the numeric PID in the host
// namespace, where that number may belong to an unrelated process.
func (r Resolver) ResolveForMonitoring(identity ProcessIdentity) (int, error) {
	needsTranslation, err := r.NeedsTranslation(identity)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrHostPIDNotFound, err)
	}
	if hostPID, ok := r.resolveWithDecision(identity, needsTranslation); ok {
		return hostPID, nil
	}
	if needsTranslation {
		return 0, fmt.Errorf("%w: pid=%d", ErrHostPIDNotFound, identity.PID)
	}
	return identity.PID, nil
}

// ResolveMonitorPID is an alias for ResolveForMonitoring.
func (r Resolver) ResolveMonitorPID(identity ProcessIdentity) (int, error) {
	return r.ResolveForMonitoring(identity)
}

// CanResolve answers whether this consumer has a usable view of the target
// namespace. For a same-namespace identity it returns true even if the
// individual process has already disappeared; Resolve then reports that
// process as not found. For a different namespace it requires at least one
// visible process in that namespace with a readable NSpid line. If the target
// namespace is not visible at all, the result is ErrUnsupportedDirection:
// this is what happens when a sandboxed consumer tries to inspect an outer or
// sibling container. This deliberately conservative answer cannot distinguish
// a dead, now-empty isolated namespace from an unsupported direction.
func (r Resolver) CanResolve(identity ProcessIdentity) (bool, error) {
	needsTranslation, err := r.NeedsTranslation(identity)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrUnsupportedDirection, err)
	}
	if !needsTranslation {
		return true, nil
	}
	if identity.NamespaceInode == 0 {
		return false, fmt.Errorf("%w: target namespace inode is unavailable", ErrUnsupportedDirection)
	}
	if r.ListPIDs == nil || r.ReadPIDNamespaceInode == nil || r.ReadNamespacedPIDs == nil {
		return false, fmt.Errorf("%w: namespace readers are not configured", ErrUnsupportedDirection)
	}

	pids, err := r.ListPIDs()
	if err != nil {
		return false, fmt.Errorf("%w: list visible processes: %w", ErrUnsupportedDirection, err)
	}

	namespaceVisible := false
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		namespaceInode, err := r.ReadPIDNamespaceInode(pid)
		if err != nil || namespaceInode != identity.NamespaceInode {
			continue
		}
		namespaceVisible = true
		if _, err := r.ReadNamespacedPIDs(pid); err == nil {
			return true, nil
		}
	}
	if namespaceVisible {
		return false, fmt.Errorf("%w: NSpid is unreadable for the target namespace", ErrUnsupportedDirection)
	}
	return false, fmt.Errorf("%w: target namespace %d is not visible", ErrUnsupportedDirection, identity.NamespaceInode)
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
		if pid <= 0 {
			continue
		}
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

// CanResolve uses the default /proc-backed resolver.
func CanResolve(identity ProcessIdentity) (bool, error) {
	return DefaultResolver().CanResolve(identity)
}

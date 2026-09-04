// Package procfs contains small, dependency-free readers for Linux-style
// /proc process metadata. A ProcFS value has a configurable root, which keeps
// the readers usable with fixture trees in tests.
package procfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrUnsupported is returned by Linux-specific readers on non-Linux systems.
var ErrUnsupported = errors.New("procfs operation is unsupported on this platform")

// ProcStat contains the process fields needed for identity and terminal
// integration. StartTimeTicks is the Linux /proc/<pid>/stat starttime value;
// it is stable across PID reuse even when the numeric PID is not.
type ProcStat struct {
	ParentPID      int
	TTYNumber      int64
	StartTimeTicks uint64
}

// Stat is kept as a concise alias for ProcStat.
type Stat = ProcStat

// ProcFS reads a proc filesystem rooted at Root. The zero value uses /proc.
type ProcFS struct {
	Root string
}

// FS is an alias for ProcFS.
type FS = ProcFS

// New returns a ProcFS rooted at root. An empty root means the host /proc.
func New(root string) ProcFS {
	return ProcFS{Root: root}
}

// Default returns a ProcFS for the host's /proc.
func Default() ProcFS {
	return ProcFS{Root: "/proc"}
}

func (p ProcFS) root() string {
	if strings.TrimSpace(p.Root) == "" {
		return "/proc"
	}
	return p.Root
}

func (p ProcFS) pidPath(pid int, suffix string) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid pid %d", pid)
	}
	return filepath.Join(p.root(), strconv.Itoa(pid), suffix), nil
}

// ReadStat reads and parses /proc/<pid>/stat.
func (p ProcFS) ReadStat(pid int) (ProcStat, error) {
	path, err := p.pidPath(pid, "stat")
	if err != nil {
		return ProcStat{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ProcStat{}, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseStat(pid, data)
}

// ReadProcStat is a descriptive alias for ReadStat.
func (p ProcFS) ReadProcStat(pid int) (ProcStat, error) {
	return p.ReadStat(pid)
}

// ParseStat parses the contents of /proc/<pid>/stat. The command name is
// allowed to contain spaces and parentheses: the suffix is split only after
// the last closing parenthesis, as required by proc(5).
func ParseStat(pid int, data []byte) (ProcStat, error) {
	if pid <= 0 {
		return ProcStat{}, fmt.Errorf("invalid pid %d", pid)
	}

	text := strings.TrimSpace(string(data))
	closeIndex := strings.LastIndexByte(text, ')')
	if closeIndex < 0 || strings.TrimSpace(text[closeIndex+1:]) == "" {
		return ProcStat{}, fmt.Errorf("unexpected stat format for pid %d", pid)
	}

	// fields[0] is field 3 (state) because fields 1 and 2 are the PID and
	// command name. Consequently fields[1], fields[4], and fields[19] are
	// ppid, tty_nr, and starttime respectively.
	fields := strings.Fields(text[closeIndex+1:])
	if len(fields) < 20 {
		return ProcStat{}, fmt.Errorf("unexpected stat field count for pid %d: got %d, want at least 20", pid, len(fields))
	}

	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcStat{}, fmt.Errorf("parse ppid for pid %d: %w", pid, err)
	}
	ttyNumber, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("parse tty_nr for pid %d: %w", pid, err)
	}
	startTimeTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("parse starttime for pid %d: %w", pid, err)
	}

	return ProcStat{
		ParentPID:      parentPID,
		TTYNumber:      ttyNumber,
		StartTimeTicks: startTimeTicks,
	}, nil
}

// ReadPIDNamespaceInode returns the inode of /proc/<pid>/ns/pid.
func (p ProcFS) ReadPIDNamespaceInode(pid int) (uint64, error) {
	path, err := p.pidPath(pid, filepath.Join("ns", "pid"))
	if err != nil {
		return 0, err
	}
	return readPIDNamespaceInode(path, pid)
}

// PIDNamespaceInode is an alias for ReadPIDNamespaceInode.
func (p ProcFS) PIDNamespaceInode(pid int) (uint64, error) {
	return p.ReadPIDNamespaceInode(pid)
}

// ReadCurrentPIDNamespaceInode returns the namespace inode visible to the
// current process. It uses procfs's self entry rather than interpolating the
// caller's numeric PID, which keeps a ProcFS rooted at a mounted host procfs
// (for example /host/proc) internally consistent.
func (p ProcFS) ReadCurrentPIDNamespaceInode() (uint64, error) {
	path := filepath.Join(p.root(), "self", "ns", "pid")
	return readPIDNamespaceInode(path, os.Getpid())
}

// CurrentPIDNamespaceInode is an alias for ReadCurrentPIDNamespaceInode.
func (p ProcFS) CurrentPIDNamespaceInode() (uint64, error) {
	return p.ReadCurrentPIDNamespaceInode()
}

// ReadStartTimeTicks returns field 20 of the stat suffix (Linux's
// starttime field) for pid.
func (p ProcFS) ReadStartTimeTicks(pid int) (uint64, error) {
	stat, err := p.ReadStat(pid)
	if err != nil {
		return 0, err
	}
	return stat.StartTimeTicks, nil
}

// ReadNamespacedPIDs parses the NSpid line in /proc/<pid>/status. Values are
// ordered from the outermost PID namespace to the namespace containing pid.
func (p ProcFS) ReadNamespacedPIDs(pid int) ([]int, error) {
	path, err := p.pidPath(pid, "status")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseNamespacedPIDs(pid, data)
}

// ReadNSPIDs is an acronym-preserving alias for ReadNamespacedPIDs.
func (p ProcFS) ReadNSPIDs(pid int) ([]int, error) {
	return p.ReadNamespacedPIDs(pid)
}

// ParseNamespacedPIDs parses the NSpid line from a process status file.
func ParseNamespacedPIDs(pid int, data []byte) ([]int, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid pid %d", pid)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "NSpid:") {
			continue
		}
		values := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "NSpid:")))
		if len(values) == 0 {
			return nil, fmt.Errorf("NSpid has no values for pid %d", pid)
		}

		pids := make([]int, 0, len(values))
		for _, value := range values {
			pidValue, err := strconv.Atoi(value)
			if err != nil || pidValue <= 0 {
				if err == nil {
					err = fmt.Errorf("value must be positive")
				}
				return nil, fmt.Errorf("parse NSpid value %q for pid %d: %w", value, pid, err)
			}
			pids = append(pids, pidValue)
		}
		return pids, nil
	}

	return nil, fmt.Errorf("NSpid not found for pid %d", pid)
}

// ParseNSPIDs is an acronym-preserving alias for ParseNamespacedPIDs.
func ParseNSPIDs(pid int, data []byte) ([]int, error) {
	return ParseNamespacedPIDs(pid, data)
}

// ListPIDs enumerates numeric process directories below the proc root. Files
// and non-numeric entries are ignored; a missing or unreadable root is an
// error.
func (p ProcFS) ListPIDs() ([]int, error) {
	entries, err := os.ReadDir(p.root())
	if err != nil {
		return nil, fmt.Errorf("read proc root %s: %w", p.root(), err)
	}

	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

// ReadProcStat reads stat from root.
func ReadProcStat(root string, pid int) (ProcStat, error) {
	return New(root).ReadStat(pid)
}

// ReadStat reads stat from root.
func ReadStat(root string, pid int) (ProcStat, error) {
	return New(root).ReadStat(pid)
}

// ReadPIDNamespaceInode reads a PID namespace inode from root.
func ReadPIDNamespaceInode(root string, pid int) (uint64, error) {
	return New(root).ReadPIDNamespaceInode(pid)
}

// ReadNamespacedPIDs reads NSpid values from root.
func ReadNamespacedPIDs(root string, pid int) ([]int, error) {
	return New(root).ReadNamespacedPIDs(pid)
}

// ReadNSPIDs is an acronym-preserving package-level alias.
func ReadNSPIDs(root string, pid int) ([]int, error) {
	return New(root).ReadNamespacedPIDs(pid)
}

// ListPIDs enumerates process directories below root.
func ListPIDs(root string) ([]int, error) {
	return New(root).ListPIDs()
}

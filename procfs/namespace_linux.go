//go:build linux

package procfs

import (
	"fmt"
	"os"
	"syscall"
)

func readPIDNamespaceInode(path string, pid int) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat pid namespace for pid %d: %w", pid, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unexpected stat type for pid namespace %d", pid)
	}
	if stat.Ino == 0 {
		return 0, fmt.Errorf("pid namespace inode is zero for pid %d", pid)
	}
	return uint64(stat.Ino), nil
}

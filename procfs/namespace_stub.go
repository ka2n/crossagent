//go:build !linux

package procfs

func readPIDNamespaceInode(_ string, _ int) (uint64, error) {
	return 0, ErrUnsupported
}

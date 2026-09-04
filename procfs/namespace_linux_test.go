//go:build linux

package procfs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadPIDNamespaceInodeFromFixture(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "42", "ns")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(path, "pid")
	if err := os.WriteFile(fixture, []byte("namespace fixture"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(fixture)
	if err != nil {
		t.Fatal(err)
	}
	want := info.Sys().(*syscall.Stat_t).Ino
	got, err := New(root).ReadPIDNamespaceInode(42)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ReadPIDNamespaceInode() = %d, want %d", got, want)
	}
}

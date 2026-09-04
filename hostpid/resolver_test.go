package hostpid

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ka2n/crossagent/procfs"
)

func TestResolverPlainNonNamespacedPID(t *testing.T) {
	var namespaceReads, startReads int
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 100, nil },
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			namespaceReads++
			if pid != 142 {
				t.Errorf("ReadPIDNamespaceInode called for pid %d, want 142", pid)
			}
			return 100, nil
		},
		ReadStartTimeTicks: func(pid int) (uint64, error) {
			startReads++
			if pid != 142 {
				t.Errorf("ReadStartTimeTicks called for pid %d, want 142", pid)
			}
			return 900, nil
		},
		ListPIDs: func() ([]int, error) {
			t.Error("ListPIDs called before raw PID validation")
			return nil, nil
		},
	}

	got, ok := r.Resolve(ProcessIdentity{PID: 142, NamespaceInode: 100, StartTimeTicks: 900})
	if !ok || got != 142 {
		t.Fatalf("Resolve() = (%d, %t), want (142, true)", got, ok)
	}
	if namespaceReads != 1 || startReads != 1 {
		t.Fatalf("raw validation reads = namespace %d/start %d, want 1/1", namespaceReads, startReads)
	}
}

func TestResolverUsesNamespaceFirstWhenNamespaceDiffers(t *testing.T) {
	calls := []string{}
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 1, nil },
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			calls = append(calls, "namespace")
			if pid == 900 || pid == 142 {
				return 99, nil
			}
			return 0, errors.New("not found")
		},
		ReadNamespacedPIDs: func(pid int) ([]int, error) {
			calls = append(calls, "nspid")
			return []int{pid, 142}, nil
		},
		ReadStartTimeTicks: func(pid int) (uint64, error) {
			calls = append(calls, "start")
			return 777, nil
		},
		ListPIDs: func() ([]int, error) {
			calls = append(calls, "list")
			return []int{900}, nil
		},
	}

	got, ok := r.Resolve(ProcessIdentity{PID: 142, NamespaceInode: 99, StartTimeTicks: 777})
	if !ok || got != 900 {
		t.Fatalf("Resolve() = (%d, %t), want (900, true)", got, ok)
	}
	if len(calls) == 0 || calls[0] != "list" {
		t.Fatalf("resolution call order = %v, want namespace search first", calls)
	}
}

func TestResolverNamespacedPIDTwoCase(t *testing.T) {
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 4026531836, nil },
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			if pid == 3210 {
				return 4026533000, nil
			}
			return 0, errors.New("not a candidate")
		},
		ReadNamespacedPIDs: func(pid int) ([]int, error) {
			if pid != 3210 {
				t.Errorf("ReadNamespacedPIDs called for pid %d, want 3210", pid)
			}
			return []int{3210, 2}, nil
		},
		ReadStartTimeTicks: func(pid int) (uint64, error) {
			if pid != 3210 {
				t.Errorf("ReadStartTimeTicks called for pid %d, want 3210", pid)
			}
			return 123456, nil
		},
		ListPIDs: func() ([]int, error) { return []int{3210}, nil },
	}

	got, ok := r.Resolve(ProcessIdentity{
		PID:            2,
		NamespaceInode: 4026533000,
		StartTimeTicks: 123456,
	})
	if !ok || got != 3210 {
		t.Fatalf("Resolve() = (%d, %t), want (3210, true)", got, ok)
	}
}

func TestResolverRejectsPIDReuseByStartTime(t *testing.T) {
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 7, nil },
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			if pid == 142 {
				return 7, nil
			}
			return 0, errors.New("not found")
		},
		ReadStartTimeTicks: func(pid int) (uint64, error) {
			if pid == 142 {
				return 200, nil // PID 142 has been reused.
			}
			return 0, errors.New("not found")
		},
		ListPIDs: func() ([]int, error) { return nil, nil },
	}

	got, ok := r.Resolve(ProcessIdentity{PID: 142, NamespaceInode: 7, StartTimeTicks: 100})
	if ok || got != 0 {
		t.Fatalf("Resolve() = (%d, %t), want (0, false)", got, ok)
	}
}

func TestResolverMultipleCandidatesWithoutRecordedStartTime(t *testing.T) {
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 1, nil },
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			if pid == 100 || pid == 101 {
				return 99, nil
			}
			return 0, errors.New("not found")
		},
		ReadNamespacedPIDs: func(pid int) ([]int, error) {
			return []int{pid, 2}, nil
		},
		// A zero recorded start time is the explicitly degraded identity
		// for which the source algorithm retains the first candidate.
		ReadStartTimeTicks: func(int) (uint64, error) {
			t.Fatal("start time should not be read for a zero recorded start time")
			return 0, nil
		},
		ListPIDs: func() ([]int, error) { return []int{100, 101}, nil },
	}

	got, ok := r.Resolve(ProcessIdentity{PID: 2, NamespaceInode: 99})
	if !ok || got != 100 {
		t.Fatalf("Resolve() = (%d, %t), want (100, true)", got, ok)
	}
}

func TestResolverRejectsAmbiguousCandidatesWhenReaderUnavailable(t *testing.T) {
	r := Resolver{
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			if pid == 100 || pid == 101 {
				return 99, nil
			}
			return 0, errors.New("not found")
		},
		ReadNamespacedPIDs: func(int) ([]int, error) { return []int{2}, nil },
		ListPIDs:           func() ([]int, error) { return []int{100, 101}, nil },
		// nil means the host cannot compare start-time ticks. A nonzero
		// recorded value therefore cannot make two candidates unambiguous.
	}

	got, ok := r.Resolve(ProcessIdentity{PID: 2, NamespaceInode: 99, StartTimeTicks: 10})
	if ok || got != 0 {
		t.Fatalf("Resolve() = (%d, %t), want (0, false)", got, ok)
	}
}

func TestAssumeNamespacedForcesTranslationWithoutAnInode(t *testing.T) {
	currentRead := false
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) {
			currentRead = true
			return 1, nil
		},
	}

	needsTranslation, err := r.NeedsTranslation(ProcessIdentity{PID: 500, AssumeNamespaced: true})
	if err != nil || !needsTranslation {
		t.Fatalf("NeedsTranslation() = (%t, %v), want (true, nil)", needsTranslation, err)
	}
	if currentRead {
		t.Fatal("AssumeNamespaced should not require a current inode comparison")
	}
}

func TestResolverLowPIDFallbackWhenNamespaceInodeUnknown(t *testing.T) {
	currentRead := false
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) {
			currentRead = true
			return 1, nil
		},
	}

	identity := ProcessIdentity{PID: 2}
	needsTranslation, err := r.NeedsTranslation(identity)
	if err != nil || !needsTranslation {
		t.Fatalf("NeedsTranslation() = (%t, %v), want (true, nil)", needsTranslation, err)
	}
	if currentRead {
		t.Fatal("low-PID fallback should not inspect the current inode when the recorded inode is unknown")
	}
	if hostPID, ok := r.Resolve(identity); ok || hostPID != 0 {
		t.Fatalf("Resolve() = (%d, %t), want (0, false)", hostPID, ok)
	}
	if _, err := r.ResolveForMonitoring(identity); !errors.Is(err, ErrHostPIDNotFound) {
		t.Fatalf("ResolveForMonitoring() error = %v, want ErrHostPIDNotFound", err)
	}
}

func TestResolverFailClosedForUnresolvedNamespacedIdentity(t *testing.T) {
	rawPIDChecks := 0
	r := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 1, nil },
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			if pid == 2 {
				rawPIDChecks++
				return 1, nil
			}
			return 0, errors.New("not found")
		},
		ReadNamespacedPIDs: func(int) ([]int, error) { return nil, errors.New("status unavailable") },
		ReadStartTimeTicks: func(int) (uint64, error) { return 20, nil },
		ListPIDs:           func() ([]int, error) { return []int{3000}, nil },
	}

	_, err := r.ResolveForMonitoring(ProcessIdentity{
		PID:            2,
		NamespaceInode: 99,
		StartTimeTicks: 20,
	})
	if !errors.Is(err, ErrHostPIDNotFound) {
		t.Fatalf("ResolveForMonitoring() error = %v, want ErrHostPIDNotFound", err)
	}
	if rawPIDChecks == 0 {
		t.Fatal("test resolver did not exercise the raw-PID validation path")
	}
}

func TestCanResolveDistinguishesUnsupportedDirectionFromNotFound(t *testing.T) {
	identity := ProcessIdentity{PID: 2, NamespaceInode: 99, StartTimeTicks: 10}

	visibleNamespace := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 1, nil },
		ListPIDs:              func() ([]int, error) { return []int{900}, nil },
		ReadPIDNamespaceInode: func(pid int) (uint64, error) {
			if pid == 900 {
				return 99, nil
			}
			return 0, errors.New("not found")
		},
		ReadNamespacedPIDs: func(int) ([]int, error) { return []int{900, 3}, nil },
	}
	canResolve, err := visibleNamespace.CanResolve(identity)
	if err != nil || !canResolve {
		t.Fatalf("CanResolve(visible namespace) = (%t, %v), want (true, nil)", canResolve, err)
	}
	if hostPID, ok := visibleNamespace.Resolve(identity); ok || hostPID != 0 {
		t.Fatalf("Resolve(missing process) = (%d, %t), want (0, false)", hostPID, ok)
	}

	inaccessibleNamespace := Resolver{
		ReadCurrentPIDNSInode: func() (uint64, error) { return 1, nil },
		ListPIDs:              func() ([]int, error) { return []int{900}, nil },
		ReadPIDNamespaceInode: func(int) (uint64, error) { return 1, nil },
		ReadNamespacedPIDs:    func(int) ([]int, error) { return []int{900, 1}, nil },
	}
	canResolve, err = inaccessibleNamespace.CanResolve(identity)
	if canResolve || !errors.Is(err, ErrUnsupportedDirection) {
		t.Fatalf("CanResolve(inaccessible direction) = (%t, %v), want (false, ErrUnsupportedDirection)", canResolve, err)
	}
}

func TestResolverDoesNotUseUnvalidatedRawPIDForNamespacedIdentity(t *testing.T) {
	identity := ProcessIdentity{PID: 2, NamespaceInode: 99}
	got, ok := (Resolver{}).Resolve(identity)
	if ok || got != 0 {
		t.Fatalf("Resolve() = (%d, %t), want (0, false)", got, ok)
	}
	if _, err := (Resolver{}).ResolveForMonitoring(identity); !errors.Is(err, ErrHostPIDNotFound) {
		t.Fatalf("ResolveForMonitoring() error = %v, want ErrHostPIDNotFound", err)
	}
}

func TestResolverPlainFallbackForMonitoring(t *testing.T) {
	id := ProcessIdentity{PID: 500}
	got, err := (Resolver{}).ResolveForMonitoring(id)
	if err != nil || got != 500 {
		t.Fatalf("ResolveForMonitoring() = (%d, %v), want (500, nil)", got, err)
	}
}

func TestCollectFromProcFixture(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PID namespace inodes are Linux-specific")
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "42/ns/pid", "pid namespace")
	writeFixtureFile(t, root, "42/stat", "42 (agent) S 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 987654\n")

	identity, err := CollectFrom(procfsForTest(root), 42)
	if err != nil {
		t.Fatal(err)
	}
	if identity.PID != 42 || identity.NamespaceInode == 0 || identity.StartTimeTicks != 987654 {
		t.Fatalf("CollectFrom() = %+v, want pid 42, nonzero inode, start 987654", identity)
	}
}

func TestNewResolverUsesConfiguredProcFS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PID namespace inodes are Linux-specific")
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "self/ns/pid", "consumer namespace")
	writeFixtureFile(t, root, "42/ns/pid", "agent namespace")
	writeFixtureFile(t, root, "42/status", "Name:\tagent\nNSpid:\t42\t2\n")
	writeFixtureFile(t, root, "42/stat", "42 (agent) S 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 987654\n")

	fs := procfs.New(root)
	namespaceInode, err := fs.ReadPIDNamespaceInode(42)
	if err != nil {
		t.Fatal(err)
	}
	r := NewResolver(fs)
	got, ok := r.Resolve(ProcessIdentity{PID: 2, NamespaceInode: namespaceInode, StartTimeTicks: 987654})
	if !ok || got != 42 {
		t.Fatalf("configured resolver Resolve() = (%d, %t), want (42, true)", got, ok)
	}
}

func procfsForTest(root string) procfs.ProcFS {
	return procfs.New(root)
}

func writeFixtureFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

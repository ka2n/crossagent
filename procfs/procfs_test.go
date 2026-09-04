package procfs

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestReadStatFixtures(t *testing.T) {
	const validStat = "42 (agent) (weird) S 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 987654\n"

	tests := []struct {
		name      string
		contents  string
		want      ProcStat
		wantError bool
	}{
		{
			name:     "command name contains parentheses and spaces",
			contents: validStat,
			want: ProcStat{
				ParentPID:      7,
				TTYNumber:      10,
				StartTimeTicks: 987654,
			},
		},
		{
			name:      "truncated",
			contents:  "42 (agent) S 7 8",
			wantError: true,
		},
		{
			name:      "garbage",
			contents:  "not a proc stat file",
			wantError: true,
		},
		{
			name:      "bad start time",
			contents:  "42 (agent) S 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 nope",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeProcFile(t, root, 42, "stat", tt.contents)

			got, err := New(root).ReadStat(42)
			if (err != nil) != tt.wantError {
				t.Fatalf("ReadStat() error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil && got != tt.want {
				t.Fatalf("ReadStat() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReadStatMissingAndInvalidPIDReturnErrors(t *testing.T) {
	fs := New(t.TempDir())
	for _, pid := range []int{0, -1} {
		if _, err := fs.ReadStat(pid); err == nil {
			t.Errorf("ReadStat(%d) returned nil error", pid)
		}
	}
	if _, err := fs.ReadStat(42); err == nil {
		t.Error("ReadStat(missing) returned nil error")
	}
}

func TestReadNamespacedPIDsFixtures(t *testing.T) {
	tests := []struct {
		name      string
		contents  string
		want      []int
		wantError bool
	}{
		{
			name:     "nested namespaces",
			contents: "Name:\tworker\nNSpid:\t3210\t2\n",
			want:     []int{3210, 2},
		},
		{
			name:     "leading whitespace",
			contents: "Name:\tworker\n  NSpid:\t10\n",
			want:     []int{10},
		},
		{
			name:      "missing line",
			contents:  "Name:\tworker\n",
			wantError: true,
		},
		{
			name:      "malformed value",
			contents:  "NSpid:\t10\tnope\n",
			wantError: true,
		},
		{
			name:      "empty value",
			contents:  "NSpid:\n",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeProcFile(t, root, 42, "status", tt.contents)

			got, err := New(root).ReadNamespacedPIDs(42)
			if (err != nil) != tt.wantError {
				t.Fatalf("ReadNamespacedPIDs() error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ReadNamespacedPIDs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListPIDsUsesConfigurableRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"1", "20", "abc", "-2", "0"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "30"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := New(root).ListPIDs()
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 20}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPIDs() = %v, want %v", got, want)
	}

	fs := New("")
	fs.Root = root
	got, err = fs.ListPIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settable Root ListPIDs() = %v, want %v", got, want)
	}
}

func TestListPIDsMissingRootReturnsError(t *testing.T) {
	_, err := New(filepath.Join(t.TempDir(), "missing")).ListPIDs()
	if err == nil {
		t.Fatal("ListPIDs(missing root) returned nil error")
	}
}

func TestReadPIDNamespaceInodeMissingReturnsError(t *testing.T) {
	_, err := New(t.TempDir()).ReadPIDNamespaceInode(42)
	if err == nil {
		t.Fatal("ReadPIDNamespaceInode(missing) returned nil error")
	}
}

func writeProcFile(t *testing.T, root string, pid int, name, contents string) {
	t.Helper()
	path := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

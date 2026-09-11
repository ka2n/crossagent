package transcript

import (
	"bytes"
	"os"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func parseFixture(t *testing.T, name agent.Name, path string) []Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state := State{}
	var entries []Entry
	for _, line := range bytes.SplitAfter(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		entry, err := ParseLine(name, line, &state)
		if err != nil {
			t.Fatalf("parse fixture line %d: %v", len(entries)+1, err)
		}
		if !bytes.Equal(entry.Raw, line) {
			t.Fatalf("fixture line %d did not byte-roundtrip", len(entries)+1)
		}
		entries = append(entries, entry)
	}
	return entries
}

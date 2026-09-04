package agent

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNames(t *testing.T) {
	want := []Name{Claude, Codex, Pi}
	got := Names()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	got[0] = Pi
	if Names()[0] != Claude {
		t.Fatal("Names() returned a slice sharing state with the package")
	}
	for _, name := range Names() {
		if !name.Valid() {
			t.Fatalf("Names() returned invalid name %q", name)
		}
		if name.String() != string(name) {
			t.Fatalf("String() = %q, want %q", name.String(), string(name))
		}
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Name
	}{
		{"claude", Claude},
		{"  claude  ", Claude},
		{"CLAUDE", Claude},
		{"claude-code", Claude},
		{"Claude-Code", Claude},
		{"codex", Codex},
		{"codex-cli", Codex},
		{"\tCODEX-CLI\n", Codex},
		{"pi", Pi},
		{"pi-coding-agent", Pi},
	} {
		got, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q) returned error %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("Parse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseRejectsUnknown(t *testing.T) {
	for _, in := range []string{"", "   ", "bogus", "claude code", "claude:x", "cl", "pi-agent"} {
		got, err := Parse(in)
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("Parse(%q) error = %v, want ErrUnknown", in, err)
		}
		if got != "" {
			t.Fatalf("Parse(%q) = %q, want the zero Name", in, got)
		}
	}
}

func TestParseErrorNamesTheValue(t *testing.T) {
	_, err := Parse("bogus")
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("Parse error %v does not quote the rejected value", err)
	}
}

func TestValid(t *testing.T) {
	for _, name := range []Name{"", "bogus", "Claude", "claude-code"} {
		if name.Valid() {
			t.Fatalf("Name(%q).Valid() = true, want false", name)
		}
	}
}

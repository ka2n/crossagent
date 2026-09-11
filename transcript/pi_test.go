package transcript

import (
	"bytes"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestPiStructuralFixture(t *testing.T) {
	entries := parseFixture(t, agent.Pi, "testdata/pi_structural.jsonl")
	if len(entries) != 7 {
		t.Fatalf("entries = %d, want 7", len(entries))
	}

	model := entries[1]
	if model.Envelope.Provider != "anthropic" || model.Envelope.Model != "claude-sonnet" {
		t.Fatalf("model envelope = %#v", model.Envelope)
	}
	assistant := entries[2]
	if assistant.Envelope.Provider != "anthropic" || assistant.Envelope.Model != "claude-sonnet" {
		t.Fatalf("assistant envelope = %#v", assistant.Envelope)
	}

	bash := entries[3]
	if bash.Envelope.Role != "bashExecution" || len(bash.Blocks) != 1 {
		t.Fatalf("bash entry = %#v", bash)
	}
	if got := bash.Blocks[0]; got.Type != "bashExecution" || got.Name != "bash" || string(got.Arguments) != `"printf 'raw output'"` || string(got.Content) != `"raw output\n"` || !bytes.Contains(got.Raw, []byte(`"exitCode":0`)) {
		t.Fatalf("bash block = %#v", got)
	}

	compaction := entries[4]
	if len(compaction.Blocks) != 3 || compaction.Blocks[0].Path != "summary" || compaction.Blocks[0].Text != "exact compaction summary" {
		t.Fatalf("compaction blocks = %#v", compaction.Blocks)
	}
	if got := compaction.Blocks[1]; got.Path != "retainedTail" || got.Type != "user" || got.Role != "user" || string(got.Content) != `"latest request"` || !bytes.Equal(got.Raw, []byte(`{"role":"user","content":"latest request"}`)) {
		t.Fatalf("retained user = %#v", got)
	}
	if got := compaction.Blocks[2]; got.Type != "assistant" || got.Role != "assistant" || !bytes.Contains(got.Raw, []byte(`"provider":"anthropic"`)) || !bytes.Contains(got.Content, []byte(`"latest reply"`)) {
		t.Fatalf("retained assistant = %#v", got)
	}

	branch := entries[5]
	if len(branch.Blocks) != 1 || branch.Blocks[0].Type != "branch_summary" || branch.Blocks[0].ID != "assistant-1" || branch.Blocks[0].Text != "alternate branch summary" {
		t.Fatalf("branch summary = %#v", branch.Blocks)
	}
	custom := entries[6]
	if custom.Envelope.Role != "custom" || custom.Envelope.Subtype != "fixture-extension" || len(custom.Blocks) != 2 || custom.Blocks[0].Name != "fixture-extension" || custom.Blocks[0].Text != "extension context" || custom.Blocks[1].Type != "image" || !bytes.Contains(custom.Blocks[1].Raw, []byte(`"data":"AAEC"`)) {
		t.Fatalf("custom message = %#v", custom)
	}
}

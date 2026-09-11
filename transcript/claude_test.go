package transcript

import (
	"bytes"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestClaudeToolResultAndDocumentRemainNative(t *testing.T) {
	raw := []byte(`{"type":"user","sessionId":"claude-1","cwd":"/work/claude","timestamp":"2026-09-11T01:02:04Z","uuid":"entry-2","message":{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"abcdef"}},{"type":"tool_result","tool_use_id":"tool-1","is_error":false,"content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}]}}`)
	entry, err := ParseLine(agent.Claude, raw, &State{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Blocks) != 2 || entry.Blocks[0].Type != "document" || entry.Blocks[1].Type != "tool_result" {
		t.Fatalf("blocks = %#v", entry.Blocks)
	}
	if !bytes.Contains(entry.Blocks[0].Raw, []byte(`"data":"abcdef"`)) {
		t.Fatalf("document source missing: %s", entry.Blocks[0].Raw)
	}
	if entry.Blocks[1].ID != "tool-1" || string(entry.Blocks[1].Content) != `[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]` {
		t.Fatalf("tool result = %#v", entry.Blocks[1])
	}
}

func TestClaudeScalarContentAndSystemMetadataAreEntries(t *testing.T) {
	state := State{}
	messageRaw := []byte(`{"type":"user","sessionId":"claude-1","cwd":"/tmp","timestamp":"2026-09-11T01:00:00Z","message":{"role":"user","content":"  do not trim me  "}}`)
	message, err := ParseLine(agent.Claude, messageRaw, &state)
	if err != nil || len(message.Blocks) != 1 || message.Blocks[0].Text != "  do not trim me  " || string(message.Blocks[0].Content) != `"  do not trim me  "` {
		t.Fatalf("message=%#v err=%v", message, err)
	}
	systemRaw := []byte(`{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-09-11T01:00:03Z","uuid":"boundary-1","data":{"hookCount":2,"private":"kept"}}`)
	system, err := ParseLine(agent.Claude, systemRaw, &state)
	if err != nil || system.Envelope.Type != "system" || system.Envelope.Subtype != "stop_hook_summary" || system.Envelope.ID != "boundary-1" || len(system.Blocks) != 1 || !bytes.Equal(system.Blocks[0].Raw, []byte(`{"hookCount":2,"private":"kept"}`)) {
		t.Fatalf("system=%#v err=%v", system, err)
	}
}

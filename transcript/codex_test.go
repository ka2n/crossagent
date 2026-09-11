package transcript

import (
	"bytes"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestCodexStructuralFixture(t *testing.T) {
	entries := parseFixture(t, agent.Codex, "testdata/codex_structural.jsonl")
	if len(entries) != 13 {
		t.Fatalf("entries = %d, want 13", len(entries))
	}
	if got := entries[1]; got.Cwd != "/work/turn" || got.Envelope.Model != "gpt-fixture" || got.Envelope.Provider != "openai" {
		t.Fatalf("turn context = %#v", got)
	}

	for _, index := range []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
		if len(entries[index].Blocks) != 1 || entries[index].Blocks[0].Path != "payload" {
			t.Fatalf("entry %d was not structurally preserved: %#v", index, entries[index])
		}
	}
	if got := entries[2].Blocks[0]; got.Type != "agent_reasoning" || got.Text != "reasoning update" || !bytes.Contains(got.Raw, []byte(`"native_counter":1`)) {
		t.Fatalf("event reasoning = %#v", got)
	}
	if got := entries[3].Blocks[0]; got.Type != "exec_command_end" || got.CallID != "exec-1" || string(got.Arguments) != `"go test ./..."` || string(got.Content) != `"ok\n"` {
		t.Fatalf("generic event = %#v", got)
	}
	if got := entries[4].Blocks[0]; got.Type != "agent_reasoning" || got.Text != "response reasoning" || got.Signature != "opaque" {
		t.Fatalf("response reasoning = %#v", got)
	}
	if got := entries[5].Blocks[0]; got.Type != "local_shell_call" || got.ID != "shell-item" || got.CallID != "shell-1" || got.Status != "completed" || !bytes.Contains(got.Arguments, []byte(`"working_directory":"/work/turn"`)) || !bytes.Contains(got.Raw, []byte(`"native_shell":true`)) {
		t.Fatalf("local shell = %#v", got)
	}
	if got := entries[6].Blocks[0]; got.Type != "custom_tool_call" || got.CallID != "custom-1" || got.Name != "apply_patch" || string(got.Arguments) != `"*** Begin Patch"` {
		t.Fatalf("custom call = %#v", got)
	}
	if got := entries[7].Blocks[0]; got.Type != "custom_tool_call_output" || got.CallID != "custom-1" || !bytes.Contains(got.Content, []byte(`"Done!"`)) {
		t.Fatalf("custom output = %#v", got)
	}
	if got := entries[8].Blocks[0]; got.Type != "tool_search_call" || got.CallID != "search-1" || !bytes.Equal(got.Arguments, []byte(`{"query":"deploy tool","limit":5}`)) {
		t.Fatalf("tool search call = %#v", got)
	}
	if got := entries[9].Blocks[0]; got.Type != "tool_search_output" || !bytes.Contains(got.Content, []byte(`"name":"deploy"`)) {
		t.Fatalf("tool search output = %#v", got)
	}
	if got := entries[10].Blocks[0]; got.Type != "web_search_call" || !bytes.Contains(got.Arguments, []byte(`"query":"fixture query"`)) {
		t.Fatalf("web search = %#v", got)
	}
	if got := entries[11].Blocks[0]; got.Type != "image_generation_call" || string(got.Content) != `"iVBORw0KGgo="` || !bytes.Contains(got.Raw, []byte(`"revised_prompt":"fixture image"`)) {
		t.Fatalf("image generation = %#v", got)
	}
	if got := entries[12].Blocks[0]; got.Type != "future_native_variant" || !bytes.Contains(got.Raw, []byte(`"native_object":{"keep":true}`)) {
		t.Fatalf("unknown response item = %#v", got)
	}
}

func TestCodexSessionMetaClearsMissingCwd(t *testing.T) {
	state := State{SessionID: "old", Cwd: "/must/not/leak"}
	entry, err := ParseLine(agent.Codex, []byte(`{"type":"session_meta","payload":{"id":"new","model_provider":"openai"}}`), &state)
	if err != nil {
		t.Fatal(err)
	}
	if state.Cwd != "" || entry.Cwd != "" || state.SessionID != "new" {
		t.Fatalf("state = %#v entry = %#v", state, entry)
	}
}

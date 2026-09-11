package transcript

import (
	"strings"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestParseClaudeMixedVisibleContent(t *testing.T) {
	state := State{}
	line := []byte(`{"type":"assistant","sessionId":"claude-1","cwd":"/work/claude","timestamp":"2026-09-11T01:02:03.123Z","uuid":"entry-1","gitBranch":"main","message":{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"thinking","thinking":"private chain","signature":"secret-signature"},{"type":"tool_use","id":"tool-1","name":"Write","input":{"file_path":"/tmp/a.txt","content":"sensitive body"}},{"type":"tool_use","id":"tool-2","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	records, err := ParseLine(agent.Claude, line, &state)
	if err != nil || len(records) != 3 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	if state.SessionID != "claude-1" || state.Cwd != "/work/claude" {
		t.Fatalf("state = %#v", state)
	}
	if records[0].Role != "assistant" || records[0].Kind != "message" || records[0].Content != "assistant: done" || records[0].NativeID != "entry-1:text:0" {
		t.Fatalf("message = %#v", records[0])
	}
	if records[1].NativeID != "tool-1" || records[1].Content != "[Write: file_path=/tmp/a.txt (14 bytes)]" {
		t.Fatalf("write = %#v", records[1])
	}
	if records[2].NativeID != "tool-2" || records[2].Content != "[Bash: go test ./...]" {
		t.Fatalf("bash = %#v", records[2])
	}
	for _, record := range records {
		data := string(record.Data)
		for _, hidden := range []string{"private chain", "secret-signature", "sensitive body"} {
			if strings.Contains(data, hidden) {
				t.Fatalf("Data leaked %q: %s", hidden, data)
			}
		}
		if record.Metadata["git_branch"] != "main" || record.Metadata["uuid"] != "entry-1" {
			t.Fatalf("metadata = %#v", record.Metadata)
		}
	}
}

func TestParseClaudePreservesBlockOrderAndDerivedIDs(t *testing.T) {
	line := []byte(`{"type":"assistant","sessionId":"s1","cwd":"/tmp","timestamp":"2026-09-11T01:00:00Z","uuid":"entry","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"MCP","input":{"api_key":"short-secret","payload":"private"}},{"type":"text","text":"middle"},{"type":"tool_use","id":"tool-2","name":"Read","input":{"file_path":"README.md"}},{"type":"text","text":"last"}]}}`)
	records, err := ParseLine(agent.Claude, line, &State{})
	if err != nil || len(records) != 4 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	if records[0].NativeID != "tool-1" || records[1].NativeID != "entry:text:1" || records[2].NativeID != "tool-2" || records[3].NativeID != "entry:text:3" {
		t.Fatalf("ordered IDs = %q, %q, %q, %q", records[0].NativeID, records[1].NativeID, records[2].NativeID, records[3].NativeID)
	}
	for index, record := range records {
		if record.Index != index {
			t.Fatalf("record %d index = %d", index, record.Index)
		}
	}
	if records[1].Content != "assistant: middle" || records[3].Content != "assistant: last" {
		t.Fatalf("ordered content = %#v", records)
	}
	for _, record := range records {
		if strings.Contains(record.Content, "short-secret") || strings.Contains(string(record.Data), "short-secret") || strings.Contains(record.Content, "private") || strings.Contains(string(record.Data), "private") {
			t.Fatalf("generic tool leaked values: %#v", record)
		}
	}
}

func TestParseClaudeArrayToolResult(t *testing.T) {
	short := []byte(`{"type":"user","sessionId":"s1","cwd":"/tmp","timestamp":"2026-09-11T01:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}]}}`)
	records, err := ParseLine(agent.Claude, short, &State{})
	if err != nil || len(records) != 1 || records[0].Content != "line one\nline two" {
		t.Fatalf("short array result=%#v err=%v", records, err)
	}
	longText := strings.Repeat("x", 201)
	line := []byte(`{"type":"user","timestamp":"2026-09-11T01:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-2","content":[{"type":"text","text":"` + longText + `"}]}]}}`)
	records, err = ParseLine(agent.Claude, line, &State{})
	if err != nil || len(records) != 1 || records[0].Content != "[result: 201 bytes]" {
		t.Fatalf("long array result=%#v err=%v", records, err)
	}
}

func TestParseClaudeToolResultAndDocument(t *testing.T) {
	state := State{SessionID: "claude-1", Cwd: "/work/claude"}
	line := []byte(`{"type":"user","timestamp":"2026-09-11T01:02:04Z","uuid":"entry-2","message":{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"abcdef"}},{"type":"tool_result","tool_use_id":"tool-1","content":"written"}]}}`)
	records, err := ParseLine(agent.Claude, line, &state)
	if err != nil || len(records) != 2 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	if records[0].Content != "user: [document: application/pdf, 6 bytes]" || strings.Contains(string(records[0].Data), "abcdef") {
		t.Fatalf("document = %#v", records[0])
	}
	if records[1].Role != "tool" || records[1].Kind != "tool_result" || records[1].NativeID != "tool-1" || records[1].Content != "written" {
		t.Fatalf("result = %#v", records[1])
	}
}

func TestParseClaudeNoiseAndGroupingBoundary(t *testing.T) {
	state := State{}
	for _, line := range []string{
		`{"type":"progress","sessionId":"claude-1","cwd":"/work/claude","timestamp":"2026-09-11T01:00:00Z","message":"noise"}`,
		`{"type":"user","timestamp":"2026-09-11T01:00:01Z","isMeta":true,"message":{"role":"user","content":"hidden preamble"}}`,
		`{"type":"assistant","timestamp":"2026-09-11T01:00:02Z","message":{"role":"assistant","content":"[Request interrupted by user]"}}`,
	} {
		records, err := ParseLine(agent.Claude, []byte(line), &state)
		if err != nil || len(records) != 0 {
			t.Fatalf("noise records=%#v err=%v", records, err)
		}
	}
	if state.SessionID != "claude-1" || state.Cwd != "/work/claude" {
		t.Fatalf("noise header state = %#v", state)
	}
	records, err := ParseLine(agent.Claude, []byte(`{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-09-11T01:00:03Z","uuid":"boundary-1","data":"turn complete"}`), &state)
	if err != nil || len(records) != 1 || records[0].Role != "system" || records[0].Kind != "system" || records[0].Metadata["subtype"] != "stop_hook_summary" {
		t.Fatalf("boundary=%#v err=%v", records, err)
	}
}

func TestParseClaudeRequiresTimestamp(t *testing.T) {
	records, err := ParseLine(agent.Claude, []byte(`{"type":"user","sessionId":"claude-1","cwd":"/tmp","message":{"role":"user","content":"no time"}}`), &State{})
	if err != nil || len(records) != 0 {
		t.Fatalf("records=%#v err=%v", records, err)
	}
}

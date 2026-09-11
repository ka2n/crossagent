package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ka2n/crossagent/agent"
)

func TestParseLinePreservesClaudeSourceAndStructure(t *testing.T) {
	raw := []byte("  {\"type\":\"assistant\",\"sessionId\":\"claude-1\",\"cwd\":\"/work/claude\",\"timestamp\":\"2026-09-11T01:02:03.123Z\",\"uuid\":\"entry-1\",\"parentUuid\":\"parent-1\",\"requestId\":\"request-1\",\"gitBranch\":\"feature/raw\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"before\"},{\"type\":\"thinking\",\"thinking\":\"private chain\",\"signature\":\"secret-signature\"},{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"Write\",\"input\":{ \"file_path\": \"/tmp/a.txt\", \"content\": \"sensitive body\" }},{\"type\":\"text\",\"text\":\"after\"}]}}  \n")
	wantRaw := bytes.Clone(raw)
	state := State{}
	entry, err := ParseLine(agent.Claude, raw, &state)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'x'
	if !bytes.Equal(entry.Raw, wantRaw) {
		t.Fatalf("Raw changed or aliases its input:\n got %q\nwant %q", entry.Raw, wantRaw)
	}
	if entry.Agent != agent.Claude || entry.SessionID != "claude-1" || entry.Cwd != "/work/claude" {
		t.Fatalf("entry metadata = %#v", entry)
	}
	if want := time.Date(2026, 9, 11, 1, 2, 3, 123000000, time.UTC); !entry.Timestamp.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", entry.Timestamp, want)
	}
	if got := entry.Envelope; got.Type != "assistant" || got.Role != "assistant" || got.ID != "entry-1" || got.ParentID != "parent-1" || got.RequestID != "request-1" || string(got.Fields["gitBranch"]) != `"feature/raw"` {
		t.Fatalf("envelope = %#v", got)
	}
	if len(entry.Blocks) != 4 {
		t.Fatalf("blocks = %#v", entry.Blocks)
	}
	if got := []string{entry.Blocks[0].Type, entry.Blocks[1].Type, entry.Blocks[2].Type, entry.Blocks[3].Type}; !equalStrings(got, []string{"text", "thinking", "tool_use", "text"}) {
		t.Fatalf("block order = %q", got)
	}
	if got := entry.Blocks[1]; got.Text != "private chain" || got.Signature != "secret-signature" {
		t.Fatalf("thinking block = %#v", got)
	}
	if got := entry.Blocks[2]; got.ID != "tool-1" || got.Name != "Write" || string(got.Arguments) != `{ "file_path": "/tmp/a.txt", "content": "sensitive body" }` {
		t.Fatalf("tool block = %#v", got)
	}
	if !bytes.Contains(entry.Blocks[2].Raw, []byte("sensitive body")) {
		t.Fatalf("tool Raw lost arguments: %s", entry.Blocks[2].Raw)
	}
}

func TestRawBytesAreExplicitAndExcludedFromJSON(t *testing.T) {
	raw := []byte(" \t{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":[ { \"type\" : \"text\", \"text\" : \"keep spacing\" } ]}}\r\n")
	entry, err := ParseLine(agent.Pi, raw, &State{})
	if err != nil {
		t.Fatal(err)
	}
	wantEntryRaw := bytes.Clone(raw)
	wantBlockRaw := []byte(`{ "type" : "text", "text" : "keep spacing" }`)
	if !bytes.Equal(entry.Raw, wantEntryRaw) || len(entry.Blocks) != 1 || !bytes.Equal(entry.Blocks[0].Raw, wantBlockRaw) {
		t.Fatalf("raw bytes were not preserved: entry=%q block=%q", entry.Raw, entry.Blocks[0].Raw)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	if !bytes.Equal(entry.Raw, wantEntryRaw) || !bytes.Equal(entry.Blocks[0].Raw, wantBlockRaw) {
		t.Fatal("entry or block Raw aliases parser input")
	}

	entryCopy := entry.RawCopy()
	blockCopy := entry.Blocks[0].RawCopy()
	entryCopy[0], blockCopy[0] = 'x', 'x'
	if entry.Raw[0] == 'x' || entry.Blocks[0].Raw[0] == 'x' {
		t.Fatal("RawCopy aliases stored source bytes")
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if _, ok := object["raw"]; ok {
		t.Fatalf("Entry.Raw was marshaled: %s", encoded)
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(object["blocks"], &blocks); err != nil {
		t.Fatal(err)
	}
	if _, ok := blocks[0]["raw"]; ok {
		t.Fatalf("Block.Raw was marshaled: %s", encoded)
	}
	if _, ok := object["envelope"]; !ok {
		t.Fatalf("structural fields were not marshaled: %s", encoded)
	}

	lineErr := &LineError{Raw: []byte("bad-json\r\n"), Err: errors.New("bad line")}
	lineCopy := lineErr.RawCopy()
	lineCopy[0] = 'x'
	if lineErr.Raw[0] == 'x' {
		t.Fatal("LineError.RawCopy aliases stored source bytes")
	}
	encoded, err = json.Marshal(lineErr)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"Raw"`)) || bytes.Contains(encoded, []byte(`"raw"`)) {
		t.Fatalf("LineError.Raw was marshaled: %s", encoded)
	}
}

func TestParseLinePreservesCodexEnvelopesAndReasoning(t *testing.T) {
	state := State{}
	headerRaw := []byte(`{"timestamp":"2026-09-10T01:00:00Z","type":"session_meta","payload":{"id":"codex-1","cwd":"/work/repo","model_provider":"openai"}}`)
	header, err := ParseLine(agent.Codex, headerRaw, &state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(header.Raw, headerRaw) || header.SessionID != "codex-1" || header.Cwd != "/work/repo" || header.Envelope.Type != "session_meta" || header.Envelope.Provider != "openai" || len(header.Blocks) != 0 {
		t.Fatalf("header = %#v", header)
	}

	raw := []byte(`{"timestamp":"2026-09-10T01:00:03Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"short summary"}],"content":[{"type":"reasoning_text","text":"full reasoning"}],"encrypted_content":"opaque-signature"}}`)
	entry, err := ParseLine(agent.Codex, raw, &state)
	if err != nil {
		t.Fatal(err)
	}
	if entry.SessionID != "codex-1" || entry.Envelope.Type != "response_item" || entry.Envelope.Subtype != "reasoning" {
		t.Fatalf("entry = %#v", entry)
	}
	if len(entry.Blocks) != 2 || entry.Blocks[0].Path != "payload.summary" || entry.Blocks[0].Type != "summary_text" || entry.Blocks[1].Path != "payload.content" || entry.Blocks[1].Type != "reasoning_text" {
		t.Fatalf("reasoning blocks = %#v", entry.Blocks)
	}
	for _, block := range entry.Blocks {
		if block.Signature != "opaque-signature" {
			t.Fatalf("reasoning signature = %q", block.Signature)
		}
	}

	callRaw := []byte(`{"timestamp":"2026-09-10T01:00:04Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"call-1","arguments":"{\"cmd\":\"go test ./...\",\"secret\":\"keep\"}"}}`)
	call, err := ParseLine(agent.Codex, callRaw, &state)
	if err != nil {
		t.Fatal(err)
	}
	if len(call.Blocks) != 1 || call.Blocks[0].Type != "function_call" || call.Blocks[0].ID != "call-1" || call.Blocks[0].Name != "shell" || string(call.Blocks[0].Arguments) != `"{\"cmd\":\"go test ./...\",\"secret\":\"keep\"}"` {
		t.Fatalf("function call = %#v", call.Blocks)
	}

	outputRaw := []byte(`{"timestamp":"2026-09-10T01:00:05Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-1","output":"full output, not summarized"}}`)
	output, err := ParseLine(agent.Codex, outputRaw, &state)
	if err != nil || len(output.Blocks) != 1 || output.Blocks[0].ID != "call-1" || string(output.Blocks[0].Content) != `"full output, not summarized"` {
		t.Fatalf("function output=%#v err=%v", output.Blocks, err)
	}
}

func TestParseLinePreservesPiBlockOrder(t *testing.T) {
	state := State{}
	headerRaw := []byte(`{"type":"session","id":"pi-1","timestamp":"2026-09-10T00:00:00Z","cwd":"/work/pi","version":3}`)
	header, err := ParseLine(agent.Pi, headerRaw, &state)
	if err != nil || !bytes.Equal(header.Raw, headerRaw) || header.Envelope.ID != "pi-1" {
		t.Fatalf("header=%#v err=%v", header, err)
	}
	raw := []byte(`{"type":"message","id":"m1","parentId":"p1","timestamp":"2026-09-10T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"before"},{"type":"thinking","thinking":"private","thinkingSignature":"signed-thinking"},{"type":"toolCall","id":"call-1","name":"read","arguments":{"path":"README.md","offset":1}},{"type":"text","text":"after"}]}}`)
	entry, err := ParseLine(agent.Pi, raw, &state)
	if err != nil {
		t.Fatal(err)
	}
	if entry.SessionID != "pi-1" || entry.Cwd != "/work/pi" || entry.Envelope.ID != "m1" || entry.Envelope.ParentID != "p1" || entry.Envelope.Role != "assistant" {
		t.Fatalf("entry = %#v", entry)
	}
	if len(entry.Blocks) != 4 {
		t.Fatalf("blocks = %#v", entry.Blocks)
	}
	if got := []string{entry.Blocks[0].Type, entry.Blocks[1].Type, entry.Blocks[2].Type, entry.Blocks[3].Type}; !equalStrings(got, []string{"text", "thinking", "toolCall", "text"}) {
		t.Fatalf("block order = %q", got)
	}
	if entry.Blocks[1].Text != "private" || entry.Blocks[1].Signature != "signed-thinking" || string(entry.Blocks[2].Arguments) != `{"path":"README.md","offset":1}` {
		t.Fatalf("lossless blocks = %#v", entry.Blocks)
	}

	resultRaw := []byte(`{"type":"message","id":"m2","timestamp":1789000000000,"message":{"role":"toolResult","toolCallId":"call-1","toolName":"read","content":[{"type":"text","text":"full result"}]}}`)
	result, err := ParseLine(agent.Pi, resultRaw, &state)
	if err != nil || len(result.Blocks) != 1 || result.Envelope.Role != "toolResult" || result.Blocks[0].ID != "call-1" || result.Blocks[0].Name != "read" || result.Blocks[0].Text != "full result" {
		t.Fatalf("tool result=%#v err=%v", result, err)
	}
	if want := time.UnixMilli(1789000000000).UTC(); !result.Timestamp.Equal(want) {
		t.Fatalf("result timestamp=%s want=%s", result.Timestamp, want)
	}
}

func TestParseLineKeepsNonVisibleAndUnknownEntries(t *testing.T) {
	state := State{}
	for _, raw := range [][]byte{
		[]byte(`{"type":"progress","sessionId":"claude-1","cwd":"/work","timestamp":"2026-09-11T01:00:00Z","message":"internal progress"}`),
		[]byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":42}}}}`),
		[]byte(`{"type":"model_change","id":"pi-model","modelId":"gpt-x","provider":"vendor"}`),
	} {
		name := agent.Claude
		if bytes.Contains(raw, []byte("event_msg")) {
			name = agent.Codex
		} else if bytes.Contains(raw, []byte("model_change")) {
			name = agent.Pi
		}
		entry, err := ParseLine(name, raw, &state)
		if err != nil || !bytes.Equal(entry.Raw, raw) || entry.Envelope.Type == "" {
			t.Fatalf("entry=%#v err=%v", entry, err)
		}
	}
}

func TestParseLineRejectsMalformedOversizedAndUnsupportedInput(t *testing.T) {
	state := State{}
	if _, err := ParseLine(agent.Codex, []byte(`{"type":`), &state); err == nil {
		t.Fatal("malformed JSON accepted")
	}
	if _, err := ParseLine(agent.Name("gemini"), []byte(`{}`), &state); err == nil {
		t.Fatal("unsupported agent accepted")
	}
	if _, err := ParseLine(agent.Pi, make([]byte, MaxLineBytes+1), &state); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("oversized line error = %v", err)
	}
	if _, err := ParseLine(agent.Pi, []byte(" \t\n"), &state); err == nil {
		t.Fatal("blank source line accepted")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

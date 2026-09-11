package transcript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ka2n/crossagent/agent"
)

func TestReadCodexIncrementally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := strings.Join([]string{
		`{"timestamp":"2026-09-10T01:00:00Z","type":"session_meta","payload":{"id":"codex-1","cwd":"/work/repo"}}`,
		`{"timestamp":"2026-09-10T01:00:01Z","type":"event_msg","payload":{"type":"token_count"}}`,
		`{"timestamp":"2026-09-10T01:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix it"}],"encrypted_content":"hidden"}}`,
		`{"timestamp":"2026-09-10T01:00:03Z","type":"response_item","payload":{"type":"reasoning","summary":[]}}`,
		`{"timestamp":"2026-09-10T01:00:04Z","type":"response_item","payload":{"type":"function_call","name":"shell","call_id":"call-1","arguments":"{\"cmd\":\"go test ./...\"}"}}`,
	}, "\n") + "\n" + `{"timestamp":"2026-09-10T01:00:05Z","type":"response_item"`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	batch, err := Read(context.Background(), agent.Codex, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Partial || len(batch.Records) != 2 {
		t.Fatalf("batch = %#v, want two records and partial tail", batch)
	}
	if got := batch.Records[0]; got.SessionID != "codex-1" || got.Cwd != "/work/repo" || got.Content != "fix it" || got.Role != "user" {
		t.Fatalf("message = %#v", got)
	}
	if strings.Contains(string(batch.Records[0].Data), "hidden") {
		t.Fatalf("Data leaked hidden Codex field: %s", batch.Records[0].Data)
	}
	if got := batch.Records[1]; got.Kind != "tool_call" || got.NativeID != "call-1" || !strings.Contains(got.Content, "go test") {
		t.Fatalf("tool call = %#v", got)
	}
	if batch.Records[0].Offset <= 0 || batch.Records[1].Offset <= batch.Records[0].Offset {
		t.Fatalf("record offsets = %d, %d", batch.Records[0].Offset, batch.Records[1].Offset)
	}
	completeSize := int64(strings.LastIndex(body, "\n") + 1)
	if batch.NextOffset != completeSize {
		t.Fatalf("next offset = %d, want %d", batch.NextOffset, completeSize)
	}
}

func TestParsePiMessagesAndTools(t *testing.T) {
	state := State{}
	if records, err := ParseLine(agent.Pi, []byte(`{"type":"session","id":"pi-1","timestamp":"2026-09-10T00:00:00Z","cwd":"/work/pi"}`), &state); err != nil || len(records) != 0 {
		t.Fatalf("header records=%d err=%v", len(records), err)
	}
	records, err := ParseLine(agent.Pi, []byte(`{"type":"message","id":"m1","timestamp":"2026-09-10T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"I will inspect it"},{"type":"thinking","thinking":"private","thinkingSignature":"secret"},{"type":"toolCall","id":"call-1","name":"read","arguments":{"path":"README.md"}},{"type":"toolCall","id":"call-2","name":"bash","arguments":{"command":"go test ./..."}}]}}`), &state)
	if err != nil || len(records) != 3 {
		t.Fatalf("mixed records=%d err=%v", len(records), err)
	}
	record := records[1]
	if record.SessionID != "pi-1" || record.Cwd != "/work/pi" || record.Kind != "tool_call" || !strings.Contains(record.Content, "README.md") {
		t.Fatalf("record = %#v", record)
	}
	if records[0].Content != "I will inspect it" || records[1].NativeID != "call-1" || records[2].NativeID != "call-2" {
		t.Fatalf("mixed records = %#v", records)
	}
	for _, got := range records {
		if strings.Contains(string(got.Data), "private") || strings.Contains(string(got.Data), "secret") {
			t.Fatalf("Data leaked thinking fields: %s", got.Data)
		}
	}
	results, err := ParseLine(agent.Pi, []byte(`{"type":"message","id":"m2","timestamp":"2026-09-10T00:00:02Z","message":{"role":"toolResult","toolCallId":"call-2","toolName":"read","content":[{"type":"text","text":"hello"}]}}`), &state)
	if err != nil || len(results) != 1 || results[0].Role != "tool" || results[0].Content != "read: hello" || results[0].NativeID != "call-2" {
		t.Fatalf("results = %#v, err=%v", results, err)
	}
}

func TestReadHonorsCancellationAndLongLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := strings.Repeat("x", 1024*1024)
	body := `{"type":"session","id":"pi-long","cwd":"/tmp"}` + "\n" +
		`{"type":"message","timestamp":1789000000000,"message":{"role":"user","content":"` + content + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	batch, err := Read(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil || len(batch.Records) != 1 || len(batch.Records[0].Content) != len(content) {
		t.Fatalf("long line records=%d err=%v", len(batch.Records), err)
	}
	if want := time.UnixMilli(1789000000000).UTC(); !batch.Records[0].Timestamp.Equal(want) {
		t.Fatalf("timestamp = %s, want %s", batch.Records[0].Timestamp, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, agent.Pi, path, Cursor{}, State{}); err != context.Canceled {
		t.Fatalf("canceled read error = %v", err)
	}
}

func TestParseLineRejectsMalformedAndUnsupportedInput(t *testing.T) {
	state := State{}
	if _, err := ParseLine(agent.Codex, []byte(`{"type":`), &state); err == nil {
		t.Fatal("malformed JSON accepted")
	}
	if _, err := ParseLine(agent.Name("gemini"), []byte(`{"type":"user"}`), &state); err == nil {
		t.Fatal("unsupported agent accepted")
	}
	if _, err := ParseLine(agent.Pi, make([]byte, MaxLineBytes+1), &state); err != ErrLineTooLong {
		t.Fatalf("oversized line error = %v", err)
	}
}

func TestReadResetsAfterFileTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"new-session","cwd":"/new"}` + "\n" +
		`{"type":"message","timestamp":"2026-09-10T00:00:00Z","message":{"role":"user","content":"new"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	batch, err := Read(context.Background(), agent.Pi, path, Cursor{Offset: 1 << 20}, State{SessionID: "old", Cwd: "/old"})
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Truncated || len(batch.Records) != 1 || batch.Records[0].SessionID != "new-session" {
		t.Fatalf("truncated batch = %#v", batch)
	}
}

func TestReadResetsWhenHeaderFingerprintChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	old := `{"type":"session","id":"old","cwd":"/old"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"old"}}` + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Read(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	newBody := `{"type":"session","id":"replacement","cwd":"/new"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"replacement is deliberately longer"}}` + "\n"
	if err := os.WriteFile(path, []byte(newBody), 0o600); err != nil {
		t.Fatal(err)
	}
	batch, err := Read(context.Background(), agent.Pi, path, first.Cursor, first.State)
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Truncated || len(batch.Records) != 1 || batch.Records[0].SessionID != "replacement" {
		t.Fatalf("replacement batch = %#v", batch)
	}
}

func TestReadMalformedLineCanBeQuarantined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"pi-1","cwd":"/tmp"}` + "\n" + "not-json\n" +
		`{"type":"message","message":{"role":"user","content":"after"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	batch, err := Read(context.Background(), agent.Pi, path, Cursor{}, State{})
	var lineErr *LineError
	if !errors.As(err, &lineErr) {
		t.Fatalf("error = %v, want LineError", err)
	}
	continued, err := Read(context.Background(), agent.Pi, path, lineErr.Next, batch.State)
	if err != nil || len(continued.Records) != 1 || continued.Records[0].Content != "after" {
		t.Fatalf("continued = %#v, err=%v", continued, err)
	}
}

func TestReadPaginatesRecordsWithoutDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var body strings.Builder
	body.WriteString(`{"type":"session","id":"pi-pages","cwd":"/tmp"}` + "\n")
	for i := 0; i < MaxBatchRecords+2; i++ {
		fmt.Fprintf(&body, "{\"type\":\"message\",\"id\":\"m%d\",\"message\":{\"role\":\"user\",\"content\":\"%d\"}}\n", i, i)
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Read(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil || len(first.Records) != MaxBatchRecords {
		t.Fatalf("first records=%d err=%v", len(first.Records), err)
	}
	second, err := Read(context.Background(), agent.Pi, path, first.Cursor, first.State)
	if err != nil || len(second.Records) != 2 {
		t.Fatalf("second records=%d err=%v", len(second.Records), err)
	}
	if first.Records[MaxBatchRecords-1].NativeID != "m999" || second.Records[0].NativeID != "m1000" {
		t.Fatalf("page boundary = %q/%q", first.Records[MaxBatchRecords-1].NativeID, second.Records[0].NativeID)
	}
}

func TestStreamResumesInsideMultiRecordLineAfterCallbackError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"pi-stream","cwd":"/tmp"}` + "\n" +
		`{"type":"message","id":"message-1","message":{"role":"assistant","content":[{"type":"text","text":"hello"},{"type":"toolCall","id":"tool-1","name":"read","arguments":{"path":"a"}},{"type":"toolCall","id":"tool-2","name":"read","arguments":{"path":"b"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("backpressure stop")
	var first []string
	status, err := Stream(context.Background(), agent.Pi, path, Cursor{}, State{}, func(record Record) error {
		if len(first) == 1 {
			return stop
		}
		first = append(first, record.NativeID)
		return nil
	})
	if !errors.Is(err, stop) || status.Cursor.RecordIndex != 1 || status.Records != 1 {
		t.Fatalf("first status=%#v err=%v", status, err)
	}
	var resumed []string
	final, err := Stream(context.Background(), agent.Pi, path, status.Cursor, status.State, func(record Record) error {
		resumed = append(resumed, record.NativeID)
		return nil
	})
	if err != nil || len(resumed) != 2 || resumed[0] != "tool-1" || resumed[1] != "tool-2" || final.Cursor.RecordIndex != 0 {
		t.Fatalf("resumed=%#v final=%#v err=%v", resumed, final, err)
	}
}

func TestReadOversizedLineCanBeQuarantined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"pi-large","cwd":"/tmp"}` + "\n" +
		strings.Repeat("x", MaxLineBytes+1) + "\n" +
		`{"type":"message","message":{"role":"user","content":"after large"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	batch, err := Read(context.Background(), agent.Pi, path, Cursor{}, State{})
	var lineErr *LineError
	if !errors.As(err, &lineErr) || !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("error = %v, want oversized LineError", err)
	}
	continued, err := Read(context.Background(), agent.Pi, path, lineErr.Next, batch.State)
	if err != nil || len(continued.Records) != 1 || continued.Records[0].Content != "after large" {
		t.Fatalf("continued = %#v, err=%v", continued, err)
	}
}

func TestConcisePreservesUTF8(t *testing.T) {
	value := strings.Repeat("界", 2000)
	got := concise(value)
	if !strings.HasSuffix(got, "…") || !utf8.ValidString(got) {
		t.Fatalf("invalid truncation suffix=%t utf8=%t", strings.HasSuffix(got, "…"), utf8.ValidString(got))
	}
}

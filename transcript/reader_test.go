package transcript

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestReaderRequiresAckAndResumesWithinLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"pi-reader","cwd":"/tmp"}` + "\n" +
		`{"type":"message","id":"m1","message":{"role":"assistant","content":[{"type":"text","text":"hello"},{"type":"toolCall","id":"tool-1","name":"read","arguments":{"path":"a"}},{"type":"toolCall","id":"tool-2","name":"read","arguments":{"path":"b"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.Next()
	if err != nil || first.Content != "hello" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	beforeAck := reader.Cursor()
	if _, err := reader.Next(); err != ErrUnackedRecord {
		t.Fatalf("second Next error=%v", err)
	}
	if reader.Cursor() != beforeAck {
		t.Fatal("unacknowledged cursor advanced")
	}
	if err := reader.Ack(); err != nil {
		t.Fatal(err)
	}
	checkpoint, state := reader.Cursor(), reader.State()
	if checkpoint.RecordIndex != 1 {
		t.Fatalf("record index=%d", checkpoint.RecordIndex)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err = Open(context.Background(), agent.Pi, path, checkpoint, state)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, want := range []string{"tool-1", "tool-2"} {
		record, err := reader.Next()
		if err != nil || record.NativeID != want {
			t.Fatalf("record=%#v err=%v want=%s", record, err, want)
		}
		if err := reader.Ack(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final error=%v", err)
	}
	if reader.Cursor().RecordIndex != 0 {
		t.Fatalf("final cursor=%#v", reader.Cursor())
	}
}

func TestReaderPoisonLineAdvancesRecoverableCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"pi-reader","cwd":"/tmp"}` + "\n" + "bad-json\n" +
		`{"type":"message","message":{"role":"user","content":"after"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Next(); err == nil {
		t.Fatalf("poison error=%v", err)
	} else {
		var lineErr *LineError
		if !errors.As(err, &lineErr) || reader.Cursor() != lineErr.Next {
			t.Fatalf("line error=%v cursor=%#v", err, reader.Cursor())
		}
	}
	record, err := reader.Next()
	if err != nil || record.Content != "after" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
}

func TestReaderOversizedPartialTailReopensAndQuarantines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","id":"pi-tail","cwd":"/tmp"}` + "\n"
	if err := os.WriteFile(path, []byte(header+string(bytes.Repeat([]byte{'x'}, MaxLineBytes+1))), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) || !reader.Partial() {
		t.Fatalf("partial error=%v status=%#v", err, reader.Status())
	}
	cursor, state := reader.Cursor(), reader.State()
	_ = reader.Close()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n" + `{"type":"message","message":{"role":"user","content":"after tail"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	reader, err = Open(context.Background(), agent.Pi, path, cursor, state)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("oversized terminated error=%v", err)
	}
	record, err := reader.Next()
	if err != nil || record.Content != "after tail" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
}

func TestReaderReplacementAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	old := `{"type":"session","id":"old","cwd":"/old"}` + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	cursor, state := reader.Cursor(), reader.State()
	_ = reader.Close()
	newBody := `{"type":"session","id":"replacement","cwd":"/new"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"new"}}` + "\n"
	if err := os.WriteFile(path, []byte(newBody), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err = Open(context.Background(), agent.Pi, path, cursor, state)
	if err != nil {
		t.Fatal(err)
	}
	if !reader.Truncated() {
		t.Fatal("replacement not detected")
	}
	record, err := reader.Next()
	if err != nil || record.SessionID != "replacement" {
		t.Fatalf("record=%#v err=%v", record, err)
	}
	_ = reader.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader, err = Open(ctx, agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
}

func TestReaderSourceOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owned, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	file := owned.source.(*os.File)
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err == nil {
		t.Fatal("Open-owned file remained open")
	}

	source := bytes.NewReader([]byte("{}\n"))
	borrowed, err := NewReader(context.Background(), agent.Pi, source, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	if err := borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("NewReader source was closed: %v", err)
	}
}

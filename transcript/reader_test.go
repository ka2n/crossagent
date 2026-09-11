package transcript

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ka2n/crossagent/agent"
)

func TestReaderYieldsEveryLineRequiresAckAndResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"type":"session","id":"pi-reader","cwd":"/tmp"}` + "\n",
		` {"type":"model_change","id":"model-1","modelId":"gpt-x","provider":"vendor"} ` + "\n",
		`{"type":"message","id":"m1","message":{"role":"assistant","content":[{"type":"text","text":"hello"},{"type":"toolCall","id":"tool-1","name":"read","arguments":{"path":"a"}}]}}` + "\r\n",
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}

	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.Next()
	if err != nil || first.Envelope.Type != "session" || first.Offset != 0 || !bytes.Equal(first.Raw, []byte(lines[0])) {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	beforeAck := reader.Cursor()
	if _, err := reader.Next(); !errors.Is(err, ErrUnackedEntry) {
		t.Fatalf("second Next error=%v", err)
	}
	if reader.Cursor() != beforeAck || reader.State() != (State{}) {
		t.Fatal("unacknowledged checkpoint advanced")
	}
	if err := reader.Ack(); err != nil {
		t.Fatal(err)
	}
	if reader.State() != (State{SessionID: "pi-reader", Cwd: "/tmp"}) {
		t.Fatalf("state = %#v", reader.State())
	}

	second, err := reader.Next()
	if err != nil || second.Envelope.Type != "model_change" || second.Offset != int64(len(lines[0])) || !bytes.Equal(second.Raw, []byte(lines[1])) {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	checkpoint, state := reader.Cursor(), reader.State()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	// Closing without Ack leaves the checkpoint on the same source line.
	reader, err = Open(context.Background(), agent.Pi, path, checkpoint, state)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	redelivered, err := reader.Next()
	if err != nil || !bytes.Equal(redelivered.Raw, second.Raw) || redelivered.Offset != second.Offset {
		t.Fatalf("redelivered=%#v err=%v", redelivered, err)
	}
	if err := reader.Ack(); err != nil {
		t.Fatal(err)
	}
	third, err := reader.Next()
	if err != nil || third.Offset != int64(len(lines[0])+len(lines[1])) || !bytes.Equal(third.Raw, []byte(lines[2])) || len(third.Blocks) != 2 {
		t.Fatalf("third=%#v err=%v", third, err)
	}
	if err := reader.Ack(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) || reader.Partial() {
		t.Fatalf("final error=%v status=%#v", err, reader.Status())
	}
	if reader.Cursor().Offset != int64(len(strings.Join(lines, ""))) {
		t.Fatalf("cursor = %#v", reader.Cursor())
	}
	if err := reader.Ack(); !errors.Is(err, ErrNoPendingEntry) {
		t.Fatalf("Ack after EOF = %v", err)
	}
}

func TestReaderPartialTailIsRetriedAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","id":"pi-tail","cwd":"/tmp"}` + "\n"
	partial := `{"type":"message","id":"m1","message":{"role":"user","content":"hel`
	if err := os.WriteFile(path, []byte(header+partial), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Ack(); err != nil {
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
	if _, err := file.WriteString(`lo"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	reader, err = Open(context.Background(), agent.Pi, path, cursor, state)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entry, err := reader.Next()
	if err != nil || entry.Offset != int64(len(header)) || len(entry.Blocks) != 1 || entry.Blocks[0].Text != "hello" || string(entry.Raw) != partial+`lo"}}`+"\n" {
		t.Fatalf("entry=%#v err=%v", entry, err)
	}
}

func TestReaderAcceptsExactFDFC880CursorFingerprint(t *testing.T) {
	header := []byte("  {\"type\":\"session\",\"id\":\"legacy\",\"cwd\":\"/legacy\"} \r\n")
	message := []byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"resume\"}}\n")
	source := bytes.NewReader(append(bytes.Clone(header), message...))
	cursor := Cursor{
		Offset:      int64(len(header)),
		Fingerprint: "fa2aa0853acbf81deeead98c37c2f1e108cb9db37a95e32baa692a3043146355",
		RecordIndex: 2,
	}
	reader, err := NewReader(context.Background(), agent.Pi, source, cursor, State{SessionID: "legacy", Cwd: "/legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if reader.Truncated() {
		t.Fatal("fdfc880 fingerprint was rejected")
	}
	if reader.Cursor().RecordIndex != 0 {
		t.Fatalf("legacy record index was not migrated: %#v", reader.Cursor())
	}
	entry, err := reader.Next()
	if err != nil || entry.Offset != int64(len(header)) || len(entry.Blocks) != 1 || entry.Blocks[0].Text != "resume" {
		t.Fatalf("entry=%#v err=%v", entry, err)
	}
}

func TestReaderRestoresCheckpointAfterSourceReadError(t *testing.T) {
	header := []byte("{\"type\":\"session\",\"id\":\"fault\",\"cwd\":\"/work\"}\n")
	message := []byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"after\"}}\n")
	source := &faultReadSeeker{Reader: bytes.NewReader(append(bytes.Clone(header), message...))}
	checkpoint := Cursor{Offset: int64(len(header)), Fingerprint: legacyFingerprintLine(header)}
	state := State{SessionID: "fault", Cwd: "/work"}
	reader, err := NewReader(context.Background(), agent.Pi, source, checkpoint, state)
	if err != nil {
		t.Fatal(err)
	}
	source.arm(11)
	if _, err := reader.Next(); !errors.Is(err, errInjectedRead) {
		t.Fatalf("injected read error = %v", err)
	}
	if reader.Cursor() != checkpoint || reader.State() != state {
		t.Fatalf("checkpoint changed after read fault: %#v %#v", reader.Cursor(), reader.State())
	}
	entry, err := reader.Next()
	if err != nil || !bytes.Equal(entry.Raw, message) || entry.Offset != int64(len(header)) || entry.SessionID != "fault" {
		t.Fatalf("retried entry=%#v err=%v", entry, err)
	}
}

func TestReaderBecomesUnusableWhenReadCheckpointCannotBeRestored(t *testing.T) {
	header := []byte("{\"type\":\"session\",\"id\":\"fault\"}\n")
	message := []byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"after\"}}\n")
	source := &faultReadSeeker{Reader: bytes.NewReader(append(bytes.Clone(header), message...))}
	checkpoint := Cursor{Offset: int64(len(header)), Fingerprint: legacyFingerprintLine(header)}
	reader, err := NewReader(context.Background(), agent.Pi, source, checkpoint, State{SessionID: "fault"})
	if err != nil {
		t.Fatal(err)
	}
	source.failRestore = true
	source.arm(7)
	if _, err := reader.Next(); !errors.Is(err, ErrReaderUnusable) || !errors.Is(err, errInjectedRead) || !errors.Is(err, errInjectedSeek) {
		t.Fatalf("unrestorable read error = %v", err)
	}
	if _, err := reader.Next(); !errors.Is(err, ErrReaderUnusable) {
		t.Fatalf("subsequent read error = %v", err)
	}
}

func TestReaderUnterminatedFirstLineGetsFingerprintAfterCompletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	partial := `{"type":"session","id":"pi-first"`
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
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
	if cursor.Fingerprint != "" {
		t.Fatalf("partial first-line fingerprint = %q", cursor.Fingerprint)
	}
	_ = reader.Close()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`,"cwd":"/tmp"}` + "\n")
	_ = file.Close()

	reader, err = Open(context.Background(), agent.Pi, path, cursor, state)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if reader.Truncated() {
		t.Fatal("completing the first source line was reported as replacement")
	}
	entry, err := reader.Next()
	if err != nil || entry.SessionID != "pi-first" || entry.Cwd != "/tmp" || reader.Cursor().Fingerprint == "" {
		t.Fatalf("entry=%#v status=%#v err=%v", entry, reader.Status(), err)
	}
}

func TestReaderMalformedLineCanBeQuarantined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","id":"pi-errors","cwd":"/tmp"}` + "\n"
	bad := "not-json \t\n"
	after := `{"type":"message","message":{"role":"user","content":"after"}}` + "\n"
	if err := os.WriteFile(path, []byte(header+bad+after), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Ack(); err != nil {
		t.Fatal(err)
	}
	_, err = reader.Next()
	var lineErr *LineError
	if !errors.As(err, &lineErr) {
		t.Fatalf("error = %v, want LineError", err)
	}
	if lineErr.Offset != int64(len(header)) || lineErr.Length != int64(len(bad)) || !bytes.Equal(lineErr.Raw, []byte(bad)) || lineErr.Next != reader.Cursor() {
		t.Fatalf("line error = %#v cursor=%#v", lineErr, reader.Cursor())
	}
	if reader.State().SessionID != "pi-errors" {
		t.Fatalf("state changed on malformed line: %#v", reader.State())
	}
	entry, err := reader.Next()
	if err != nil || entry.Blocks[0].Text != "after" || entry.Offset != int64(len(header)+len(bad)) {
		t.Fatalf("entry=%#v err=%v", entry, err)
	}
}

func TestReaderOversizedLineCanBeQuarantined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","id":"pi-large","cwd":"/tmp"}` + "\n"
	large := strings.Repeat("x", MaxLineBytes) + "\n"
	after := `{"type":"message","message":{"role":"user","content":"after large"}}` + "\n"
	if err := os.WriteFile(path, []byte(header+large+after), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Ack(); err != nil {
		t.Fatal(err)
	}
	_, err = reader.Next()
	var lineErr *LineError
	if !errors.As(err, &lineErr) || !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("error = %v, want oversized LineError", err)
	}
	if lineErr.Offset != int64(len(header)) || lineErr.Length != int64(len(large)) || lineErr.Raw != nil || lineErr.Next != reader.Cursor() {
		t.Fatalf("line error = %#v cursor=%#v", lineErr, reader.Cursor())
	}
	entry, err := reader.Next()
	if err != nil || entry.Blocks[0].Text != "after large" {
		t.Fatalf("entry=%#v err=%v", entry, err)
	}
}

func TestReaderOversizedPartialTailWaitsForTermination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session","id":"pi-tail","cwd":"/tmp"}` + "\n"
	if err := os.WriteFile(path, append([]byte(header), bytes.Repeat([]byte{'x'}, MaxLineBytes+1)...), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(context.Background(), agent.Pi, path, Cursor{}, State{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Ack(); err != nil {
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
	_, _ = file.WriteString("\n")
	_ = file.Close()
	reader, err = Open(context.Background(), agent.Pi, path, cursor, state)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("terminated tail error=%v", err)
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
	if _, err := reader.Next(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Ack(); err != nil {
		t.Fatal(err)
	}
	cursor, state := reader.Cursor(), reader.State()
	_ = reader.Close()

	newBody := ` {"type":"session","id":"replacement","cwd":"/new"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"new"}}` + "\n"
	if err := os.WriteFile(path, []byte(newBody), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err = Open(context.Background(), agent.Pi, path, cursor, state)
	if err != nil {
		t.Fatal(err)
	}
	if !reader.Truncated() || reader.Cursor().Offset != 0 || reader.State() != (State{}) {
		t.Fatalf("replacement status=%#v", reader.Status())
	}
	entry, err := reader.Next()
	if err != nil || entry.SessionID != "replacement" || entry.Offset != 0 {
		t.Fatalf("entry=%#v err=%v", entry, err)
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

var errInjectedRead = errors.New("injected source read failure")
var errInjectedSeek = errors.New("injected source seek failure")

type faultReadSeeker struct {
	*bytes.Reader
	armed       bool
	failed      bool
	failAfter   int
	failRestore bool
}

func (r *faultReadSeeker) arm(after int) {
	r.armed = true
	r.failAfter = after
}

func (r *faultReadSeeker) Read(p []byte) (int, error) {
	if !r.armed || r.failed {
		return r.Reader.Read(p)
	}
	if len(p) > r.failAfter {
		p = p[:r.failAfter]
	}
	n, err := r.Reader.Read(p)
	if n == r.failAfter {
		r.failed = true
		return n, errInjectedRead
	}
	return n, err
}

func (r *faultReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if r.failRestore && r.failed {
		return 0, errInjectedSeek
	}
	return r.Reader.Seek(offset, whence)
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

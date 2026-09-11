// Package transcript normalizes the append-only JSONL transcripts written by
// coding agents. It contains no indexing or persistence policy; callers decide
// which files and offsets to retain.
package transcript

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ka2n/crossagent/agent"
)

// State carries transcript header metadata into later records.
type State struct {
	SessionID string
	Cwd       string
}

// Record is one searchable conversation or tool event. Data contains only the
// visible vendor event represented by the record.
type Record struct {
	Agent     agent.Name      `json:"agent"`
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	Offset    int64           `json:"offset"`
	Timestamp time.Time       `json:"timestamp"`
	Role      string          `json:"role"`
	Kind      string          `json:"kind"`
	Content   string          `json:"content"`
	NativeID  string          `json:"native_id,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// ParseLine updates state and converts one JSONL line. It returns no records
// for headers, reasoning, token accounting, and other internal events.
func ParseLine(name agent.Name, line []byte, state *State) ([]Record, error) {
	if state == nil {
		return nil, errors.New("transcript: nil state")
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	if len(line) > MaxLineBytes {
		return nil, ErrLineTooLong
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("transcript: decode %s line: %w", name, err)
	}
	var records []Record
	switch name {
	case agent.Codex:
		records = parseCodex(raw, state)
	case agent.Pi:
		records = parsePi(raw, state)
	default:
		return nil, fmt.Errorf("transcript: unsupported agent %q", name)
	}
	for i := range records {
		records[i].Agent, records[i].SessionID, records[i].Cwd = name, state.SessionID, state.Cwd
	}
	return records, nil
}

const (
	// MaxLineBytes bounds one vendor JSON object.
	MaxLineBytes = 16 << 20
	// MaxBatchRecords bounds records returned by one Read call.
	MaxBatchRecords = 1000
	// MaxBatchBytes bounds complete source bytes consumed by one Read call.
	MaxBatchBytes = 64 << 20
)

var ErrLineTooLong = errors.New("transcript: JSONL line exceeds 16 MiB")
var ErrBatchTooManyRecords = errors.New("transcript: one JSONL line exceeds batch record limit")

// Batch is the result of reading complete JSONL records from an offset.
type Batch struct {
	Records []Record
	Cursor  Cursor
	// NextOffset mirrors Cursor.Offset for callers that only display progress.
	NextOffset int64
	Partial    bool
	Truncated  bool
	State      State
}

// Cursor identifies both a byte position and the transcript generation whose
// header was observed there.
type Cursor struct {
	Offset      int64
	Fingerprint string
}

// LineError reports a complete bad line that callers may quarantine. Retrying
// from Next.Offset continues at the following line.
type LineError struct {
	Next Cursor
	Err  error
}

func (e *LineError) Error() string { return e.Err.Error() }
func (e *LineError) Unwrap() error { return e.Err }

// Read reads complete lines beginning at offset. An unterminated final line is
// left unconsumed and reported through Partial, so a later call can retry it.
func Read(ctx context.Context, name agent.Name, path string, cursor Cursor, state State) (Batch, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cursor.Offset < 0 {
		return Batch{}, errors.New("transcript: negative offset")
	}
	f, err := os.Open(path)
	if err != nil {
		return Batch{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Batch{}, err
	}
	fingerprint, err := headerFingerprint(f)
	if err != nil {
		return Batch{}, err
	}
	truncated := cursor.Offset > info.Size() || cursor.Fingerprint != "" && cursor.Fingerprint != fingerprint
	if truncated {
		cursor.Offset, state = 0, State{}
	}
	cursor.Fingerprint = fingerprint
	if _, err := f.Seek(cursor.Offset, io.SeekStart); err != nil {
		return Batch{}, err
	}
	startOffset := cursor.Offset
	b := Batch{Cursor: cursor, NextOffset: cursor.Offset, State: state, Truncated: truncated}
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		if len(b.Records) >= MaxBatchRecords || b.NextOffset-startOffset >= MaxBatchBytes {
			return b, nil
		}
		if err := ctx.Err(); err != nil {
			return b, err
		}
		line, consumed, readErr := readLine(r)
		if errors.Is(readErr, io.EOF) {
			b.Partial = len(line) > 0
			return b, nil
		}
		if errors.Is(readErr, ErrLineTooLong) {
			next := Cursor{Offset: b.NextOffset + consumed, Fingerprint: fingerprint}
			return b, &LineError{Next: next, Err: readErr}
		}
		if readErr != nil {
			return b, readErr
		}
		if b.NextOffset-startOffset+int64(len(line)) > MaxBatchBytes {
			return b, nil
		}
		previousState := b.State
		records, err := ParseLine(name, line, &b.State)
		if err != nil {
			next := Cursor{Offset: b.NextOffset + int64(len(line)), Fingerprint: fingerprint}
			return b, &LineError{Next: next, Err: err}
		}
		if len(b.Records)+len(records) > MaxBatchRecords {
			b.State = previousState
			return b, ErrBatchTooManyRecords
		}
		for i := range records {
			records[i].Offset = b.NextOffset
		}
		b.NextOffset += int64(len(line))
		b.Cursor.Offset = b.NextOffset
		b.Records = append(b.Records, records...)
	}
}

func readLine(r *bufio.Reader) ([]byte, int64, error) {
	var line []byte
	var consumed int64
	tooLong := false
	for {
		part, err := r.ReadSlice('\n')
		consumed += int64(len(part))
		if !tooLong && len(line)+len(part) > MaxLineBytes {
			line, tooLong = nil, true
		} else if !tooLong {
			line = append(line, part...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if tooLong && err == nil {
			return nil, consumed, ErrLineTooLong
		}
		return line, consumed, err
	}
}

func headerFingerprint(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	line, _, err := readLine(bufio.NewReaderSize(f, 64*1024))
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSpace(line))
	return fmt.Sprintf("%x", sum), nil
}

func parseTime(raw json.RawMessage) time.Time {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		t, _ := time.Parse(time.RFC3339Nano, text)
		return t
	}
	var millis int64
	if json.Unmarshal(raw, &millis) == nil {
		return time.UnixMilli(millis).UTC()
	}
	return time.Time{}
}

func text(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func contentText(raw json.RawMessage) string {
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		return direct
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" || block.Type == "input_text" || block.Type == "output_text" {
			if value := strings.TrimSpace(block.Text); value != "" {
				parts = append(parts, value)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func concise(value string) string {
	value = strings.TrimSpace(value)
	const limit = 4096
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		return value + "…"
	}
	return value
}

func visibleData(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

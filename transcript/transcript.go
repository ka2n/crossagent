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
	Index     int             `json:"index"`
	Timestamp time.Time       `json:"timestamp"`
	Role      string          `json:"role"`
	Kind      string          `json:"kind"`
	Content   string          `json:"content"`
	NativeID  string          `json:"native_id,omitempty"`
	Metadata  map[string]any  `json:"metadata,omitempty"`
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
	case agent.Claude:
		records = parseClaude(raw, state)
	case agent.Codex:
		records = parseCodex(raw, state)
	case agent.Pi:
		records = parsePi(raw, state)
	default:
		return nil, fmt.Errorf("transcript: unsupported agent %q", name)
	}
	for i := range records {
		records[i].Agent, records[i].SessionID, records[i].Cwd = name, state.SessionID, state.Cwd
		records[i].Index = i
	}
	return records, nil
}

const (
	// MaxLineBytes bounds one vendor JSON object.
	MaxLineBytes = 16 << 20
	// MaxBatchRecords bounds records delivered by one Stream or Read call.
	MaxBatchRecords = 1000
	// MaxBatchBytes bounds complete source bytes consumed by one Read call.
	MaxBatchBytes = 64 << 20
)

var ErrLineTooLong = errors.New("transcript: JSONL line exceeds 16 MiB")

// Batch is the result of reading complete JSONL records from an offset.
type Batch struct {
	Records []Record
	Cursor  Cursor
	// NextOffset mirrors Cursor.Offset for callers that only display progress.
	NextOffset   int64
	Partial      bool
	Truncated    bool
	LimitReached bool
	State        State
}

// Cursor identifies both a byte position and the transcript generation whose
// header was observed there.
type Cursor struct {
	Offset      int64
	Fingerprint string
	RecordIndex int
}

// LineError reports a complete bad line that callers may quarantine. Retrying
// from Next.Offset continues at the following line.
type LineError struct {
	Next Cursor
	Err  error
}

func (e *LineError) Error() string { return e.Err.Error() }
func (e *LineError) Unwrap() error { return e.Err }

// Status describes where a transcript read stopped.
type Status struct {
	Cursor       Cursor
	State        State
	Partial      bool
	Truncated    bool
	LimitReached bool
	Records      int
}

// StreamStatus is a descriptive alias for Stream callers.
type StreamStatus = Status

// Stream synchronously delivers records with natural callback backpressure.
// Its cursor advances after each successful callback; RecordIndex permits an
// interrupted multi-record line to resume without redelivering accepted data.
func Stream(ctx context.Context, name agent.Name, path string, cursor Cursor, state State, yield func(Record) error) (StreamStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if yield == nil {
		return StreamStatus{}, errors.New("transcript: nil stream callback")
	}
	if cursor.Offset < 0 {
		return StreamStatus{}, errors.New("transcript: negative offset")
	}
	if cursor.RecordIndex < 0 {
		return StreamStatus{}, errors.New("transcript: negative record index")
	}
	f, err := os.Open(path)
	if err != nil {
		return StreamStatus{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return StreamStatus{}, err
	}
	fingerprint, err := headerFingerprint(f)
	if err != nil {
		return StreamStatus{}, err
	}
	truncated := cursor.Offset > info.Size() || cursor.Fingerprint != "" && cursor.Fingerprint != fingerprint
	if truncated {
		cursor.Offset, cursor.RecordIndex, state = 0, 0, State{}
	}
	cursor.Fingerprint = fingerprint
	if _, err := f.Seek(cursor.Offset, io.SeekStart); err != nil {
		return StreamStatus{}, err
	}
	startOffset := cursor.Offset
	status := StreamStatus{Cursor: cursor, State: state, Truncated: truncated}
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		if status.Records >= MaxBatchRecords || status.Cursor.Offset-startOffset >= MaxBatchBytes {
			status.LimitReached = true
			return status, nil
		}
		if err := ctx.Err(); err != nil {
			return status, err
		}
		line, consumed, hasData, readErr := readLine(r)
		if errors.Is(readErr, io.EOF) {
			status.Partial = hasData
			return status, nil
		}
		if errors.Is(readErr, ErrLineTooLong) {
			next := Cursor{Offset: status.Cursor.Offset + consumed, Fingerprint: fingerprint}
			return status, &LineError{Next: next, Err: readErr}
		}
		if readErr != nil {
			return status, readErr
		}
		if status.Cursor.Offset-startOffset+int64(len(line)) > MaxBatchBytes {
			status.LimitReached = true
			return status, nil
		}
		records, err := ParseLine(name, line, &status.State)
		if err != nil {
			next := Cursor{Offset: status.Cursor.Offset + int64(len(line)), Fingerprint: fingerprint}
			return status, &LineError{Next: next, Err: err}
		}
		if status.Cursor.RecordIndex > len(records) {
			return status, errors.New("transcript: cursor record index exceeds line records")
		}
		for i := range records {
			records[i].Offset = status.Cursor.Offset
		}
		for i := status.Cursor.RecordIndex; i < len(records); i++ {
			if status.Records >= MaxBatchRecords {
				status.LimitReached = true
				return status, nil
			}
			if err := yield(records[i]); err != nil {
				return status, err
			}
			status.Records++
			status.Cursor.RecordIndex = i + 1
		}
		status.Cursor.Offset += int64(len(line))
		status.Cursor.RecordIndex = 0
	}
}

// Read is a bounded convenience wrapper around Stream.
func Read(ctx context.Context, name agent.Name, path string, cursor Cursor, state State) (Batch, error) {
	batch := Batch{}
	status, err := Stream(ctx, name, path, cursor, state, func(record Record) error {
		batch.Records = append(batch.Records, record)
		return nil
	})
	batch.Cursor = status.Cursor
	batch.NextOffset = status.Cursor.Offset
	batch.Partial = status.Partial
	batch.Truncated = status.Truncated
	batch.LimitReached = status.LimitReached
	batch.State = status.State
	return batch, err
}

func readLine(r *bufio.Reader) ([]byte, int64, bool, error) {
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
			return nil, consumed, true, ErrLineTooLong
		}
		return line, consumed, consumed > 0, err
	}
}

func headerFingerprint(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	line, _, _, err := readLine(bufio.NewReaderSize(f, 64*1024))
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return fingerprintLine(line), nil
}

func fingerprintLine(line []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(line))
	return fmt.Sprintf("%x", sum)
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
		return conciseBytes(value, limit) + "…"
	}
	return value
}

func conciseBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func visibleData(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

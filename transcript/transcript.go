// Package transcript reads the append-only JSONL transcripts written by
// coding agents. It preserves source bytes and exposes vendor structure without
// applying search, visibility, redaction, or persistence policy.
package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ka2n/crossagent/agent"
)

// State carries transcript-level metadata into subsequent source lines.
type State struct {
	SessionID string
	Cwd       string
}

// Entry is one complete, newline-terminated source line. Raw is an owned,
// byte-for-byte copy of that line, including its line ending. ParseLine also
// accepts data without a line ending and preserves exactly what it receives.
// Raw is deliberately excluded from JSON marshaling: source bytes are not a
// JSON value and must be stored or transmitted explicitly when required.
type Entry struct {
	Agent     agent.Name `json:"agent"`
	Offset    int64      `json:"offset"`
	Raw       []byte     `json:"-"`
	Timestamp time.Time  `json:"timestamp"`
	SessionID string     `json:"session_id,omitempty"`
	Cwd       string     `json:"cwd,omitempty"`
	Envelope  Envelope   `json:"envelope"`
	Blocks    []Block    `json:"blocks,omitempty"`
}

// RawCopy returns a caller-owned copy of the exact source-line bytes.
func (e Entry) RawCopy() []byte { return cloneBytes(e.Raw) }

// Envelope is common metadata from the vendor's outer object. Type and
// Subtype retain native names; they are not mapped to a common event taxonomy.
// Fields contains every native top-level field as its original JSON value.
type Envelope struct {
	Type      string                     `json:"type,omitempty"`
	Subtype   string                     `json:"subtype,omitempty"`
	Role      string                     `json:"role,omitempty"`
	ID        string                     `json:"id,omitempty"`
	ParentID  string                     `json:"parent_id,omitempty"`
	RequestID string                     `json:"request_id,omitempty"`
	Model     string                     `json:"model,omitempty"`
	Provider  string                     `json:"provider,omitempty"`
	Fields    map[string]json.RawMessage `json:"fields"`
}

// Block is an ordered, content-bearing vendor structure. Type is the native
// block or payload type. Raw is an owned, byte-exact copy of the complete
// native block and is deliberately excluded from JSON marshaling. Arguments
// and Content retain native JSON values as structural accessors; Text and
// Signature are decoded strings. Path identifies the native field containing
// the block.
type Block struct {
	Path      string          `json:"path,omitempty"`
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	ID        string          `json:"id,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Status    string          `json:"status,omitempty"`
	Text      string          `json:"text,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Raw       []byte          `json:"-"`
}

// RawCopy returns a caller-owned copy of the exact native block bytes.
func (b Block) RawCopy() []byte { return cloneBytes(b.Raw) }

const (
	// MaxLineBytes bounds one complete source line, including its line ending.
	MaxLineBytes = 16 << 20
)

var ErrLineTooLong = errors.New("transcript: JSONL line exceeds 16 MiB")

// Cursor identifies a byte position and the transcript generation whose first
// source line was observed. Offset always points to a source-line boundary.
type Cursor struct {
	Offset      int64
	Fingerprint string
	// RecordIndex is retained so persisted pre-lossless cursors still decode.
	// Deprecated: entries are now acknowledged by source line. NewReader ignores
	// this value and may redeliver the one line named by an old cursor.
	RecordIndex int
}

// LineError identifies a complete malformed or oversized line that callers
// may quarantine. Next is already positioned at the following source line.
// Raw is populated for malformed JSON. Oversized lines are not retained in
// memory; Offset and Length identify their source byte range.
type LineError struct {
	Offset int64
	Length int64
	Raw    []byte `json:"-"`
	Next   Cursor
	Err    error
}

func (e *LineError) Error() string { return e.Err.Error() }
func (e *LineError) Unwrap() error { return e.Err }

// RawCopy returns a caller-owned copy of the malformed source-line bytes.
func (e *LineError) RawCopy() []byte {
	if e == nil {
		return nil
	}
	return cloneBytes(e.Raw)
}

// Status reports the acknowledged checkpoint and terminal file flags.
type Status struct {
	Cursor    Cursor
	State     State
	Partial   bool
	Truncated bool
}

// ParseLine parses one source line and updates transcript state. It never
// filters an entry or reconstructs its Raw bytes. Vendor fields with unknown
// shapes remain available through Entry.Raw and Block.Raw.
func ParseLine(name agent.Name, line []byte, state *State) (Entry, error) {
	if state == nil {
		return Entry{}, errors.New("transcript: nil state")
	}
	if name != agent.Claude && name != agent.Codex && name != agent.Pi {
		return Entry{}, fmt.Errorf("transcript: unsupported agent %q", name)
	}
	if len(line) > MaxLineBytes {
		return Entry{}, ErrLineTooLong
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{}, fmt.Errorf("transcript: decode %s line: %w", name, err)
	}
	entry := Entry{Agent: name, Raw: cloneBytes(line)}
	switch name {
	case agent.Claude:
		parseClaude(raw, state, &entry)
	case agent.Codex:
		parseCodex(raw, state, &entry)
	case agent.Pi:
		parsePi(raw, state, &entry)
	}
	entry.SessionID, entry.Cwd = state.SessionID, state.Cwd
	return entry, nil
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

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) != 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return cloneRaw(value)
		}
	}
	return nil
}

func cloneRaw(raw []byte) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func cloneBytes(raw []byte) []byte {
	return append([]byte(nil), raw...)
}

type blockDefaults struct {
	role      string
	id        string
	name      string
	signature string
}

func contentBlocks(raw json.RawMessage, path, fallbackType string, defaults blockDefaults) []Block {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		blocks := make([]Block, 0, len(items))
		for _, item := range items {
			blocks = append(blocks, nativeBlock(item, path, fallbackType, defaults))
		}
		return blocks
	}
	return []Block{nativeBlock(raw, path, fallbackType, defaults)}
}

func nativeBlock(raw json.RawMessage, path, fallbackType string, defaults blockDefaults) Block {
	block := Block{Path: path, Type: fallbackType, Role: defaults.role, ID: defaults.id, Name: defaults.name, Signature: defaults.signature, Raw: cloneBytes(raw)}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		block.Text = text(raw)
		block.Content = cloneRaw(raw)
		return block
	}
	block.Type = first(text(fields["type"]), fallbackType)
	block.Role = first(text(fields["role"]), defaults.role)
	block.CallID = first(text(fields["call_id"]), text(fields["toolCallId"]), text(fields["tool_use_id"]), defaults.id)
	block.ID = first(text(fields["id"]), block.CallID, defaults.id)
	block.Name = first(text(fields["name"]), text(fields["toolName"]), defaults.name)
	block.Status = text(fields["status"])
	block.Text = first(text(fields["text"]), text(fields["thinking"]), text(fields["message"]), text(fields["query"]), text(fields["summary"]))
	block.Signature = first(text(fields["signature"]), text(fields["thinkingSignature"]), text(fields["encrypted_content"]), defaults.signature)
	block.Arguments = firstRaw(fields["arguments"], fields["input"], fields["action"], fields["command"])
	block.Content = firstRaw(fields["content"], fields["output"], fields["result"], fields["tools"], fields["aggregated_output"])
	return block
}

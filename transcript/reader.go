package transcript

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ka2n/crossagent/agent"
)

var ErrUnackedEntry = errors.New("transcript: acknowledge the current entry before calling Next")
var ErrNoPendingEntry = errors.New("transcript: no entry to acknowledge")
var ErrReaderUnusable = errors.New("transcript: reader is unusable after source read failure")

// Reader pulls one source-line Entry at a time while keeping its acknowledged
// checkpoint separate from the entry currently being handled.
type Reader struct {
	ctx       context.Context
	name      agent.Name
	source    io.ReadSeeker
	closer    io.Closer
	buffer    *bufio.Reader
	cursor    Cursor
	state     State
	partial   bool
	truncated bool
	exhausted bool
	unusable  error

	pending      bool
	pendingNext  Cursor
	pendingState State
}

// Open opens path and returns a Reader that owns the file.
func Open(ctx context.Context, name agent.Name, path string, cursor Cursor, state State) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader, err := NewReader(ctx, name, file, cursor, state)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	reader.closer = file
	return reader, nil
}

// NewReader reads from source without taking ownership of it.
func NewReader(ctx context.Context, name agent.Name, source io.ReadSeeker, cursor Cursor, state State) (*Reader, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if source == nil {
		return nil, errors.New("transcript: nil reader source")
	}
	if name != agent.Claude && name != agent.Codex && name != agent.Pi {
		return nil, fmt.Errorf("transcript: unsupported agent %q", name)
	}
	if cursor.Offset < 0 {
		return nil, errors.New("transcript: negative cursor")
	}
	size, err := source.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	fingerprint, err := seekerFingerprint(source)
	if err != nil {
		return nil, err
	}
	truncated := cursor.Offset > size || cursor.Fingerprint != "" && cursor.Fingerprint != fingerprint
	if truncated {
		cursor.Offset, state = 0, State{}
	}
	// RecordIndex belonged to the pre-lossless, multi-record-per-line API.
	// A migrated cursor resumes at its source-line offset and may redeliver that
	// line, which is safer than skipping source bytes.
	cursor.RecordIndex = 0
	cursor.Fingerprint = fingerprint
	if _, err := source.Seek(cursor.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	return &Reader{
		ctx: ctx, name: name, source: source,
		buffer: bufio.NewReaderSize(source, 64*1024),
		cursor: cursor, state: state, truncated: truncated,
	}, nil
}

// Next returns the next complete source-line entry. The caller must call Ack
// after accepting it; until then Cursor and State remain at the previous
// acknowledged checkpoint.
func (r *Reader) Next() (Entry, error) {
	if r.pending {
		return Entry{}, ErrUnackedEntry
	}
	if r.unusable != nil {
		return Entry{}, errors.Join(ErrReaderUnusable, r.unusable)
	}
	if r.exhausted {
		return Entry{}, io.EOF
	}
	if err := r.ctx.Err(); err != nil {
		return Entry{}, err
	}

	line, consumed, hasData, readErr := readLine(r.ctx, r.buffer)
	if errors.Is(readErr, io.EOF) {
		r.partial = hasData
		r.exhausted = true
		return Entry{}, io.EOF
	}
	if errors.Is(readErr, ErrLineTooLong) {
		next := Cursor{Offset: r.cursor.Offset + consumed, Fingerprint: r.cursor.Fingerprint}
		lineErr := &LineError{Offset: r.cursor.Offset, Length: consumed, Next: next, Err: readErr}
		r.cursor = next
		return Entry{}, lineErr
	}
	if readErr != nil {
		if _, seekErr := r.source.Seek(r.cursor.Offset, io.SeekStart); seekErr != nil {
			r.unusable = errors.Join(readErr, fmt.Errorf("restore acknowledged offset: %w", seekErr))
			return Entry{}, errors.Join(ErrReaderUnusable, r.unusable)
		}
		r.buffer.Reset(r.source)
		return Entry{}, readErr
	}

	working := r.state
	entry, err := ParseLine(r.name, line, &working)
	if err != nil {
		next := Cursor{Offset: r.cursor.Offset + consumed, Fingerprint: r.cursor.Fingerprint}
		lineErr := &LineError{Offset: r.cursor.Offset, Length: consumed, Raw: append([]byte(nil), line...), Next: next, Err: err}
		r.cursor = next
		return Entry{}, lineErr
	}
	entry.Offset = r.cursor.Offset
	r.pending = true
	r.pendingNext = Cursor{Offset: r.cursor.Offset + consumed, Fingerprint: r.cursor.Fingerprint}
	r.pendingState = working
	return entry, nil
}

// Ack marks the last entry returned by Next as accepted.
func (r *Reader) Ack() error {
	if !r.pending {
		return ErrNoPendingEntry
	}
	r.cursor, r.state = r.pendingNext, r.pendingState
	r.pending = false
	return nil
}

func (r *Reader) Cursor() Cursor  { return r.cursor }
func (r *Reader) State() State    { return r.state }
func (r *Reader) Partial() bool   { return r.partial }
func (r *Reader) Truncated() bool { return r.truncated }

// Status returns the acknowledged checkpoint and terminal file flags.
func (r *Reader) Status() Status {
	return Status{Cursor: r.cursor, State: r.state, Partial: r.partial, Truncated: r.truncated}
}

// Close closes a file opened by Open. It is a no-op for NewReader sources.
func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

func readLine(ctx context.Context, r *bufio.Reader) ([]byte, int64, bool, error) {
	var line []byte
	var consumed int64
	tooLong := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, consumed, consumed > 0, err
		}
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

func seekerFingerprint(source io.ReadSeeker) (string, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	line, _, _, err := readLine(context.Background(), bufio.NewReaderSize(source, 64*1024))
	if errors.Is(err, io.EOF) {
		// Without a complete first line there is no stable transcript
		// identity yet.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return legacyFingerprintLine(line), nil
}

// legacyFingerprintLine is the exact fdfc880 cursor algorithm. Keep it stable:
// persisted consumers use this value to detect transcript replacement.
func legacyFingerprintLine(line []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(line))
	return fmt.Sprintf("%x", sum)
}

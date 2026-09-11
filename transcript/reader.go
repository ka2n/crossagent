package transcript

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"

	"github.com/ka2n/crossagent/agent"
)

var ErrUnackedRecord = errors.New("transcript: acknowledge the current record before calling Next")
var ErrNoPendingRecord = errors.New("transcript: no record to acknowledge")

// Reader pulls normalized records from one transcript while keeping an
// acknowledged checkpoint separate from the record currently being handled.
type Reader struct {
	ctx       context.Context
	name      agent.Name
	source    io.ReadSeeker
	closer    io.Closer
	buffer    *bufio.Reader
	cursor    Cursor
	state     State
	working   State
	partial   bool
	truncated bool
	exhausted bool

	records      []Record
	recordIndex  int
	lineBytes    int64
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
	if cursor.Offset < 0 || cursor.RecordIndex < 0 {
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
		cursor.Offset, cursor.RecordIndex, state = 0, 0, State{}
	}
	cursor.Fingerprint = fingerprint
	if _, err := source.Seek(cursor.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	return &Reader{
		ctx: ctx, name: name, source: source,
		buffer: bufio.NewReaderSize(source, 64*1024),
		cursor: cursor, state: state, working: state, truncated: truncated,
	}, nil
}

// Next returns the next record. The caller must call Ack after accepting it;
// until then Cursor and State remain at the previous acknowledged checkpoint.
func (r *Reader) Next() (Record, error) {
	if r.pending {
		return Record{}, ErrUnackedRecord
	}
	if r.exhausted {
		return Record{}, io.EOF
	}
	for {
		if err := r.ctx.Err(); err != nil {
			return Record{}, err
		}
		if r.recordIndex < len(r.records) {
			record := r.records[r.recordIndex]
			next := r.cursor
			next.RecordIndex = r.recordIndex + 1
			if next.RecordIndex == len(r.records) {
				next.Offset += r.lineBytes
				next.RecordIndex = 0
			}
			r.pending, r.pendingNext, r.pendingState = true, next, r.working
			return record, nil
		}

		line, consumed, hasData, readErr := readLine(r.buffer)
		if errors.Is(readErr, io.EOF) {
			r.partial = hasData
			r.exhausted = true
			return Record{}, io.EOF
		}
		if errors.Is(readErr, ErrLineTooLong) {
			next := Cursor{Offset: r.cursor.Offset + consumed, Fingerprint: r.cursor.Fingerprint}
			r.cursor = next
			return Record{}, &LineError{Next: next, Err: readErr}
		}
		if readErr != nil {
			return Record{}, readErr
		}
		r.working = r.state
		records, err := ParseLine(r.name, line, &r.working)
		if err != nil {
			next := Cursor{Offset: r.cursor.Offset + int64(len(line)), Fingerprint: r.cursor.Fingerprint}
			r.cursor = next
			return Record{}, &LineError{Next: next, Err: err}
		}
		if r.cursor.RecordIndex > len(records) {
			return Record{}, errors.New("transcript: cursor record index exceeds line records")
		}
		for index := range records {
			records[index].Offset = r.cursor.Offset
		}
		r.records, r.recordIndex, r.lineBytes = records, r.cursor.RecordIndex, int64(len(line))
		if len(records) == 0 {
			r.cursor.Offset += r.lineBytes
			r.cursor.RecordIndex = 0
			r.state = r.working
			r.records = nil
		}
	}
}

// Ack marks the last record returned by Next as accepted.
func (r *Reader) Ack() error {
	if !r.pending {
		return ErrNoPendingRecord
	}
	r.cursor, r.state = r.pendingNext, r.pendingState
	r.recordIndex++
	if r.cursor.RecordIndex == 0 {
		r.records, r.recordIndex, r.lineBytes = nil, 0, 0
	}
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

func seekerFingerprint(source io.ReadSeeker) (string, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	line, _, _, err := readLine(bufio.NewReaderSize(source, 64*1024))
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return fingerprintLine(line), nil
}

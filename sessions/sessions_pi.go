package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PiSessionLister enumerates pi's JSONL session files under its configured
// session directory. It does not depend on pi being installed or running.
type PiSessionLister struct {
	options SessionListerOptions
}

// NewPiSessionLister creates a pi session lister.
func NewPiSessionLister(options SessionListerOptions) *PiSessionLister {
	return &PiSessionLister{options: options}
}

type piSessionHeader struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

func (l *PiSessionLister) List(ctx context.Context) ([]Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionsDir := l.options.resolver().PiSessionRoot()
	var sessions []Session
	err := filepath.WalkDir(sessionsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		id, ok := parsePiSessionName(entry.Name())
		if !ok {
			return nil
		}
		header, ok := readPiSessionHeader(path)
		if !ok || strings.TrimSpace(header.Cwd) == "" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		lastActivity := info.ModTime()
		if lastActivity.IsZero() {
			lastActivity = parseTextTime(header.Timestamp)
		}
		sessions = append(sessions, Session{
			Agent:        "pi",
			SessionID:    id,
			Cwd:          header.Cwd,
			Label:        filepath.Base(filepath.Clean(header.Cwd)),
			LastActivity: lastActivity,
			Source:       SourcePiSession,
		})
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("scan pi sessions: %w", err)
	}
	return finishResult(l.options, sessions), nil
}

func readPiSessionHeader(path string) (piSessionHeader, bool) {
	var header piSessionHeader
	if !readFirstLine(path, func(line []byte) bool {
		if json.Unmarshal(line, &header) != nil {
			return false
		}
		return header.Type == "session" && strings.TrimSpace(header.ID) != ""
	}) {
		return piSessionHeader{}, false
	}
	return header, true
}

func parsePiSessionName(name string) (string, bool) {
	base := strings.TrimSuffix(name, ".jsonl")
	separator := strings.LastIndexByte(base, '_')
	if separator < 0 || separator == len(base)-1 {
		return "", false
	}
	id := strings.TrimSpace(base[separator+1:])
	if id == "" {
		return "", false
	}
	return id, true
}

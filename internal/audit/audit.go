// Package audit writes `lazydeck serve`'s per-action audit log (issue
// #49): one JSON object per line recording who did what, to which device,
// with what result, and when.
//
// The log is append-only from lazydeck's side: the file is opened with
// O_APPEND, never truncated, rotated, or rewritten by lazydeck, and each
// entry is a single write of one complete line so concurrent entries never
// interleave. Entries carry identities (key id and name, or the local user
// behind the session token) and never credentials: no bearer tokens, API
// keys, key digests, webhook URLs, or request bodies beyond the specific
// fields below. For tamper resistance against the user who owns the file,
// put it on storage that user can't rewrite, or set the append-only
// attribute (`chattr +a`, which needs root).
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Actor kinds.
const (
	ActorSession   = "session"   // the per-launch token from the connection file
	ActorAPIKey    = "api_key"   // a long-lived key from `lazydeck api-key create`
	ActorAnonymous = "anonymous" // no valid credential was presented
)

// Results.
const (
	ResultOK           = "ok"
	ResultAccepted     = "accepted" // an async job was queued; its outcome follows as a job.finished entry
	ResultError        = "error"
	ResultDenied       = "denied"       // authenticated, but the credential lacks the scope
	ResultUnauthorized = "unauthorized" // no valid credential
)

// Entry is one audit record.
type Entry struct {
	Time       time.Time `json:"time"`
	Actor      string    `json:"actor"`                // ActorSession, ActorAPIKey, or ActorAnonymous
	ActorID    string    `json:"actor_id,omitempty"`   // API key id (public handle, not the secret)
	ActorName  string    `json:"actor_name,omitempty"` // API key name, or the OS user for the session
	Action     string    `json:"action"`
	Device     string    `json:"device,omitempty"`
	GameID     string    `json:"game_id,omitempty"`
	Directory  string    `json:"directory,omitempty"` // local build directory deployed or synced into
	JobID      string    `json:"job_id,omitempty"`
	Operation  string    `json:"operation,omitempty"` // the job's operation, on job.finished entries
	Result     string    `json:"result"`
	Status     int       `json:"status,omitempty"` // HTTP status, for request entries
	ErrorKind  string    `json:"error_kind,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	RemoteAddr string    `json:"remote_addr,omitempty"`
}

// Log serializes entries to an append-only sink.
type Log struct {
	mu sync.Mutex
	w  io.Writer
	f  *os.File // set when w is a file we opened, for Sync/Close
}

// DefaultPath returns $LAZYDECK_AUDIT_LOG if set, otherwise
// $XDG_STATE_HOME/lazydeck/audit.log (falling back to
// ~/.local/state/lazydeck/audit.log).
func DefaultPath() (string, error) {
	if p := os.Getenv("LAZYDECK_AUDIT_LOG"); p != "" {
		return p, nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "lazydeck", "audit.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving audit log location: %w", err)
	}
	return filepath.Join(home, ".local", "state", "lazydeck", "audit.log"), nil
}

// Open opens (creating if needed) the audit log at path for appending, with
// mode 0600 enforced even on a pre-existing file. Callers should fail
// startup if this fails rather than serve mutations unaudited.
func Open(path string) (*Log, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening audit log %s: %w", path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return &Log{w: f, f: f}, nil
}

// New wraps an arbitrary writer, for tests.
func New(w io.Writer) *Log { return &Log{w: w} }

// Write appends e as one line. A zero Time is set to now.
func (l *Log) Write(e Entry) error {
	if l == nil {
		return errors.New("audit log is not open")
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Time = e.Time.UTC()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.w.Write(line); err != nil {
		return fmt.Errorf("writing audit entry: %w", err)
	}
	if l.f != nil {
		if err := l.f.Sync(); err != nil {
			return fmt.Errorf("syncing audit log: %w", err)
		}
	}
	return nil
}

// Close closes the underlying file, if any.
func (l *Log) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriteNormalizesTimeToUTCAndStampsZeroTime(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	pst := time.FixedZone("PST", -8*3600)
	if err := l.Write(Entry{Time: time.Date(2026, 11, 20, 1, 0, 0, 0, pst), Actor: ActorAPIKey, Action: "deploy", Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := l.Write(Entry{Action: "health", Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSON lines, got %d: %q", len(lines), buf.String())
	}
	var first, second Entry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lines[0], `"time":"2026-11-20T09:00:00Z"`) {
		t.Errorf("first entry time not normalized to UTC: %s", lines[0])
	}
	if second.Time.IsZero() || second.Time.Before(before.Add(-time.Second)) {
		t.Errorf("zero time was not stamped with now: %v", second.Time)
	}
	if first.Actor != ActorAPIKey || first.Action != "deploy" {
		t.Errorf("first entry = %+v", first)
	}
}

func TestNilLogFailsLoudly(t *testing.T) {
	var l *Log
	if err := l.Write(Entry{Action: "deploy"}); err == nil {
		t.Fatal("Write on a nil Log must fail so mutations are never silently unaudited")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close on a nil Log: %v", err)
	}
	if err := New(&bytes.Buffer{}).Close(); err != nil {
		t.Fatalf("Close on a writer-backed Log: %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteSurfacesSinkErrors(t *testing.T) {
	if err := New(failingWriter{}).Write(Entry{Action: "deploy"}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want the sink's error", err)
	}
}

func TestOpenFailsWhenParentIsAFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(blocker, "audit.log")); err == nil {
		t.Fatal("Open under a regular file: expected error")
	}
}

func TestWriteClosedFileFails(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Write(Entry{Action: "deploy"}); err == nil {
		t.Fatal("Write after Close: expected error")
	}
}

func TestConcurrentWritesStayLineAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Write(Entry{Action: "deploy", Reason: strings.Repeat("x", 200+i)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	for i, line := range lines {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not a complete JSON entry: %v", i, err)
		}
	}
}

func TestDefaultPathFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYDECK_AUDIT_LOG", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "lazydeck", "audit.log"); got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

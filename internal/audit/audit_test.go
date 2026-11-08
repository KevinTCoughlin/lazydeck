package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenAppendsAndNeverTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "audit.log")

	l, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := l.Write(Entry{Actor: ActorSession, Action: "deploy", Device: "deck", Result: ResultAccepted}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// A second Open (a serve restart) must append, not truncate.
	l, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := l.Write(Entry{Actor: ActorAPIKey, ActorID: "abc", Action: "logs.sync", Result: ResultError}); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var entries []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q is not one JSON entry: %v", sc.Text(), err)
		}
		entries = append(entries, e)
	}
	if len(entries) != 2 || entries[0].Action != "deploy" || entries[1].Action != "logs.sync" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Time.IsZero() {
		t.Error("Write should stamp a zero Time")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("audit log mode = %o, want 600", mode)
		}
	}
}

func TestOpenTightensPreexistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{}\n" {
		t.Fatalf("existing content changed: %q", data)
	}
}

func TestDefaultPathHonorsOverrides(t *testing.T) {
	t.Setenv("LAZYDECK_AUDIT_LOG", "")
	t.Setenv("XDG_STATE_HOME", "/state")
	if got, _ := DefaultPath(); got != filepath.Join("/state", "lazydeck", "audit.log") {
		t.Errorf("XDG_STATE_HOME: got %q", got)
	}
	t.Setenv("LAZYDECK_AUDIT_LOG", "/x/audit.jsonl")
	if got, _ := DefaultPath(); got != "/x/audit.jsonl" {
		t.Errorf("LAZYDECK_AUDIT_LOG: got %q", got)
	}
}

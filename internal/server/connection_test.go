package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireWriteReadConnectionInfoRoundTrip(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path, err := connectionFilePath()
	if err != nil {
		t.Fatalf("connectionFilePath: %v", err)
	}

	f, err := acquireConnectionFile(path)
	if err != nil {
		t.Fatalf("acquireConnectionFile: %v", err)
	}
	defer func() { _ = f.Close() }()

	info := ConnectionInfo{PID: os.Getpid(), Port: 12345, BaseURL: "http://127.0.0.1:12345", Token: "secret", APIVersion: APIVersion}
	if err := writeConnectionInfo(f, info); err != nil {
		t.Fatalf("writeConnectionInfo: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("connection file perm = %o, want 0600 (it holds a bearer token)", perm)
	}

	got, err := readConnectionInfo(path)
	if err != nil {
		t.Fatalf("readConnectionInfo: %v", err)
	}
	if got != info {
		t.Fatalf("got %#v, want %#v", got, info)
	}
}

func TestAcquireConnectionFileRejectsConcurrentInstance(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path, err := connectionFilePath()
	if err != nil {
		t.Fatalf("connectionFilePath: %v", err)
	}

	first, err := acquireConnectionFile(path)
	if err != nil {
		t.Fatalf("first acquireConnectionFile: %v", err)
	}
	defer func() { _ = first.Close() }()

	if _, err := acquireConnectionFile(path); err == nil {
		t.Fatal("expected a second acquireConnectionFile on the same path to fail while the first is held")
	}
}

func TestAcquireConnectionFileSucceedsAfterRelease(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path, err := connectionFilePath()
	if err != nil {
		t.Fatalf("connectionFilePath: %v", err)
	}

	first, err := acquireConnectionFile(path)
	if err != nil {
		t.Fatalf("first acquireConnectionFile: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := acquireConnectionFile(path)
	if err != nil {
		t.Fatalf("second acquireConnectionFile after release: %v", err)
	}
	_ = second.Close()
}

func TestAcquireConnectionFileEnforcesModeOnPreexistingFile(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path, err := connectionFilePath()
	if err != nil {
		t.Fatalf("connectionFilePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("seeding a pre-existing 0644 file: %v", err)
	}

	f, err := acquireConnectionFile(path)
	if err != nil {
		t.Fatalf("acquireConnectionFile: %v", err)
	}
	defer func() { _ = f.Close() }()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want acquireConnectionFile to enforce 0600 even on a pre-existing file", perm)
	}
}

func TestConnectionFilePathPrefersXDGRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	path, err := connectionFilePath()
	if err != nil {
		t.Fatalf("connectionFilePath: %v", err)
	}
	want := filepath.Join(dir, "lazydeck", "serve.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

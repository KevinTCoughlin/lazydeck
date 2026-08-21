package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadConnectionInfoRoundTrip(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	info := ConnectionInfo{PID: os.Getpid(), Port: 12345, BaseURL: "http://127.0.0.1:12345", Token: "secret", APIVersion: APIVersion}

	path, err := writeConnectionInfo(info)
	if err != nil {
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

func TestCheckNoOtherInstanceAllowsStaleFile(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	// A PID essentially guaranteed not to be a live process on this machine.
	if _, err := writeConnectionInfo(ConnectionInfo{PID: 1 << 30, Port: 1}); err != nil {
		t.Fatalf("writeConnectionInfo: %v", err)
	}
	if err := checkNoOtherInstance(); err != nil {
		t.Fatalf("checkNoOtherInstance with a stale (dead-pid) file: %v", err)
	}
}

func TestCheckNoOtherInstanceRejectsLiveProcess(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if _, err := writeConnectionInfo(ConnectionInfo{PID: os.Getpid(), Port: 1}); err != nil {
		t.Fatalf("writeConnectionInfo: %v", err)
	}
	if err := checkNoOtherInstance(); err == nil {
		t.Fatal("expected an error when the connection file names this (live) process")
	}
}

func TestCheckNoOtherInstanceAllowsMissingFile(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if err := checkNoOtherInstance(); err != nil {
		t.Fatalf("checkNoOtherInstance with no file yet: %v", err)
	}
}

func TestRemoveOwnConnectionFileOnlyRemovesMatchingPID(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	path, err := writeConnectionInfo(ConnectionInfo{PID: 999999, Port: 1})
	if err != nil {
		t.Fatalf("writeConnectionInfo: %v", err)
	}

	removeOwnConnectionFile(path, os.Getpid()) // different PID: must not remove
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file was removed despite PID mismatch: %v", err)
	}

	removeOwnConnectionFile(path, 999999) // matching PID: should remove
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, stat err = %v", err)
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

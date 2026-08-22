package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/server"
)

// withConnectionFileDir points $XDG_RUNTIME_DIR at a fresh temp dir for the
// duration of the test, so EnsureServing's connection-file lookup (via
// internal/server.ConnectionFilePath, which prefers XDG_RUNTIME_DIR) is
// isolated from any real `lazydeck serve` the host machine might be
// running.
func withConnectionFileDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return filepath.Join(dir, "lazydeck", "serve.json")
}

func writeFakeConnectionFile(t *testing.T, path, baseURL, token string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	info := server.ConnectionInfo{PID: os.Getpid(), BaseURL: baseURL, Token: token, APIVersion: "v1"}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal connection info: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write connection file: %v", err)
	}
}

func TestEnsureServing_ReusesHealthyConnectionFile(t *testing.T) {
	connPath := withConnectionFileDir(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","api_version":"v1"}`))
	}))
	defer ts.Close()

	writeFakeConnectionFile(t, connPath, ts.URL, "tok")

	info, err := EnsureServing(t.Context(), false)
	if err != nil {
		t.Fatalf("EnsureServing: %v", err)
	}
	if info.BaseURL != ts.URL {
		t.Errorf("info.BaseURL = %q, want %q (should reuse the existing healthy server)", info.BaseURL, ts.URL)
	}
}

func TestEnsureServing_UnhealthyConnectionFileIsNotOverwritten(t *testing.T) {
	connPath := withConnectionFileDir(t)
	// Port 1 is reserved and will refuse the connection immediately rather
	// than hang, without requiring a real crashed process to simulate one.
	writeFakeConnectionFile(t, connPath, "http://127.0.0.1:1", "tok")

	_, err := EnsureServing(t.Context(), false)
	if err == nil {
		t.Fatal("expected an error for an unhealthy connection file, got nil")
	}
}

func TestEnsureServing_AutoStartDisabled(t *testing.T) {
	withConnectionFileDir(t) // no connection file written
	t.Setenv("LAZYDECK_AUTOSTART", "0")

	_, err := EnsureServing(t.Context(), false)
	if err == nil {
		t.Fatal("expected an error when auto-start is disabled and no server is running")
	}
}

func TestAutoStartEnabled(t *testing.T) {
	cases := map[string]bool{
		"":      true,
		"1":     true,
		"true":  true,
		"0":     false,
		"false": false,
		"False": false,
	}
	for v, want := range cases {
		t.Setenv("LAZYDECK_AUTOSTART", v)
		if got := autoStartEnabled(); got != want {
			t.Errorf("autoStartEnabled() with LAZYDECK_AUTOSTART=%q = %v, want %v", v, got, want)
		}
	}
}

func TestHealthyTimesOutAgainstUnreachableHost(t *testing.T) {
	start := time.Now()
	ok := healthy(t.Context(), server.ConnectionInfo{BaseURL: "http://127.0.0.1:1", Token: "tok"})
	if ok {
		t.Fatal("expected healthy() to be false for an unreachable host")
	}
	if elapsed := time.Since(start); elapsed > healthCheckTimeout+time.Second {
		t.Errorf("healthy() took %s, want roughly bounded by healthCheckTimeout (%s)", elapsed, healthCheckTimeout)
	}
}

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The API key store relies on WriteFileAtomic to land at 0600 even when it
// replaces a file that was created with looser permissions.
func TestWriteFileAtomicAppliesModeOnReplace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), "api-keys.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q, want new", data)
	}
}

func TestWriteFileAtomicFailureLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	// Renaming a file over a non-empty directory fails, which exercises the
	// post-write cleanup path.
	target := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte("x"), 0o600); err == nil {
		t.Fatal("expected an error renaming over a non-empty directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicMissingDirFails(t *testing.T) {
	if err := WriteFileAtomic(filepath.Join(t.TempDir(), "nope", "f"), []byte("x"), 0o600); err == nil {
		t.Fatal("expected an error writing into a missing directory")
	}
}

func TestAPIKeysAndUserConfigPathsHonorXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	keys, err := APIKeysPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdg, "lazydeck", "api-keys.json"); keys != want {
		t.Errorf("APIKeysPath = %q, want %q", keys, want)
	}
	user, err := UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdg, "lazydeck", "config.yml"); user != want {
		t.Errorf("UserConfigPath = %q, want %q", user, want)
	}
	if info, err := os.Stat(filepath.Dir(keys)); err != nil || !info.IsDir() {
		t.Errorf("config dir was not created: %v", err)
	}
}

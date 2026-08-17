package client

import "testing"

func TestNewLocatesPythonDirViaEnv(t *testing.T) {
	t.Setenv("DEVKIT_TUI_PYTHON_DIR", "/some/custom/path")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.PythonDir != "/some/custom/path" {
		t.Errorf("expected env override to win, got %q", c.PythonDir)
	}
}

func TestNewLocatesPythonDirViaDevLayout(t *testing.T) {
	// No env override: should fall back to the repo's python/ directory
	// alongside this source file (internal/client/client.go -> ../../python).
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !isPythonDir(c.PythonDir) {
		t.Errorf("resolved PythonDir %q does not contain cli.py", c.PythonDir)
	}
}

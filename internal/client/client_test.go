package client

import (
	"errors"
	"testing"
)

func TestNewLocatesPythonDirViaEnv(t *testing.T) {
	t.Setenv("LAZYDECK_PYTHON_DIR", "/some/custom/path")
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

func TestParseEnvelopeSuccess(t *testing.T) {
	data, err := parseEnvelope([]byte(`{"ok":true,"data":{"foo":"bar"}}`), nil, nil)
	if err != nil {
		t.Fatalf("parseEnvelope: %v", err)
	}
	if string(data) != `{"foo":"bar"}` {
		t.Errorf("unexpected data: %s", data)
	}
}

func TestParseEnvelopeExplicitFailure(t *testing.T) {
	_, err := parseEnvelope([]byte(`{"ok":false,"error":"machine unreachable","error_kind":"unreachable"}`), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "machine unreachable" {
		t.Errorf("unexpected error message: %v", err)
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("expected *CLIError, got %T", err)
	}
	if cliErr.Kind != "unreachable" {
		t.Errorf("expected kind=unreachable, got %q", cliErr.Kind)
	}
}

func TestParseEnvelopeFailureWithNoMessage(t *testing.T) {
	// Guards against a cli.py bug that reports ok:false but forgets to set
	// an error string — should still surface a clear error, not "".
	_, err := parseEnvelope([]byte(`{"ok":false}`), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestParseEnvelopeNonZeroExitNoStdout(t *testing.T) {
	// Simulates uv/python crashing before printing any JSON (e.g. missing
	// interpreter, syntax error) — the subprocess error should surface,
	// including stderr, rather than a raw JSON-unmarshal error.
	runErr := errors.New("exit status 1")
	_, err := parseEnvelope(nil, []byte("Traceback (most recent call last): ..."), runErr)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, runErr) {
		t.Errorf("expected wrapped runErr, got: %v", err)
	}
}

func TestParseEnvelopeMalformedJSON(t *testing.T) {
	// Simulates cli.py printing something that isn't valid JSON at all
	// (e.g. a stray print() statement corrupting stdout) with a clean
	// exit code — should not panic, and should surface the raw output
	// for debugging.
	_, err := parseEnvelope([]byte("not json"), []byte(""), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

//go:build unix

package client

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeUV is a stand-in for the uv binary. It records the exact argv it
// received (NUL-separated, so arguments containing spaces or shell
// metacharacters stay distinguishable), its working directory and its
// UV_PROJECT_ENVIRONMENT, then prints a canned cli.py envelope.
type fakeUV struct {
	client   *Client
	argsFile string
	envFile  string
}

func newFakeUV(t *testing.T, reply string, exitCode int) *fakeUV {
	t.Helper()
	dir := t.TempDir()
	f := &fakeUV{
		argsFile: filepath.Join(dir, "args"),
		envFile:  filepath.Join(dir, "env"),
	}
	replyFile := filepath.Join(dir, "reply")
	if err := os.WriteFile(replyFile, []byte(reply), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\0' \"$@\" > '" + f.argsFile + "'\n" +
		"printf '%s\\n%s\\n' \"$PWD\" \"$UV_PROJECT_ENVIRONMENT\" > '" + f.envFile + "'\n" +
		"cat '" + replyFile + "'\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	uv := filepath.Join(dir, "uv")
	if err := os.WriteFile(uv, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.client = &Client{PythonDir: makePythonDir(t), UVPath: uv}
	return f
}

// cliArgs returns the arguments after `uv run --no-dev --project <dir>
// python cli.py`, failing if that prefix is wrong.
func (f *fakeUV) cliArgs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.argsFile)
	if err != nil {
		t.Fatalf("fake uv was not run: %v", err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	prefix := []string{"run", "--no-dev", "--project", f.client.PythonDir, "python", "cli.py"}
	if len(args) < len(prefix) || !slices.Equal(args[:len(prefix)], prefix) {
		t.Fatalf("uv argv = %q, want prefix %q", args, prefix)
	}
	return args[len(prefix):]
}

const okNull = `{"ok":true,"data":null}`

func TestCommandsBuildExactArgv(t *testing.T) {
	ctx := context.Background()
	hostile := "deck; rm -rf ~ $(id)"
	cases := []struct {
		name  string
		reply string
		call  func(c *Client) error
		want  []string
	}{
		{
			name:  "register",
			reply: okNull,
			call:  func(c *Client) error { return c.Register(ctx, "deck.local") },
			want:  []string{"register", "--machine", "deck.local"},
		},
		{
			name:  "status without login",
			reply: `{"ok":true,"data":{"battery":50}}`,
			call:  func(c *Client) error { _, err := c.Status(ctx, "deck.local", ""); return err },
			want:  []string{"status", "--machine", "deck.local"},
		},
		{
			name:  "status with login",
			reply: `{"ok":true,"data":{}}`,
			call:  func(c *Client) error { _, err := c.Status(ctx, "deck.local", "deck"); return err },
			want:  []string{"status", "--machine", "deck.local", "--login", "deck"},
		},
		{
			name:  "list games",
			reply: `{"ok":true,"data":[]}`,
			call:  func(c *Client) error { _, err := c.ListGames(ctx, "deck.local", "deck"); return err },
			want:  []string{"list-games", "--machine", "deck.local", "--login", "deck"},
		},
		{
			name:  "deploy minimal",
			reply: okNull,
			call:  func(c *Client) error { return c.Deploy(ctx, "deck.local", "", "game", "/build", false, nil) },
			want:  []string{"deploy", "--machine", "deck.local", "--name", "game", "--directory", "/build"},
		},
		{
			name:  "deploy full puts argv last",
			reply: okNull,
			call: func(c *Client) error {
				return c.Deploy(ctx, "deck.local", "deck", "game", "/my build", true, []string{"./game", "--flag", "two words"})
			},
			want: []string{"deploy", "--machine", "deck.local", "--name", "game", "--directory", "/my build",
				"--login", "deck", "--delete-extraneous", "--argv", "./game", "--flag", "two words"},
		},
		{
			name:  "delete",
			reply: okNull,
			call:  func(c *Client) error { return c.Delete(ctx, "deck.local", "deck", "game") },
			want:  []string{"delete", "--machine", "deck.local", "--name", "game", "--login", "deck"},
		},
		{
			name:  "sync logs",
			reply: okNull,
			call:  func(c *Client) error { return c.SyncLogs(ctx, "deck.local", "", "game", "/logs") },
			want:  []string{"sync-logs", "--machine", "deck.local", "--name", "game", "--directory", "/logs"},
		},
		{
			name:  "connection info",
			reply: `{"ok":true,"data":{"address":"10.0.0.2","login":"deck"}}`,
			call:  func(c *Client) error { _, err := c.ConnectionInfo(ctx, "deck.local", "deck"); return err },
			want:  []string{"connection-info", "--machine", "deck.local", "--login", "deck"},
		},
		{
			name:  "run command",
			reply: `{"ok":true,"data":{"stdout":"hi","exit_status":0}}`,
			call:  func(c *Client) error { _, err := c.RunCommand(ctx, "deck.local", "", "echo hi"); return err },
			want:  []string{"run", "--machine", "deck.local", "--cmd", "echo hi"},
		},
		{
			// Arguments go to uv as separate argv entries, never through a
			// shell, so metacharacters in a device name reach cli.py intact.
			name:  "metacharacters stay one argument",
			reply: okNull,
			call:  func(c *Client) error { return c.Register(ctx, hostile) },
			want:  []string{"register", "--machine", hostile},
		},
		{
			name:  "discover formats the timeout",
			reply: `{"ok":true,"data":[{"name":"deck","address":"10.0.0.2","port":32000}]}`,
			call:  func(c *Client) error { _, err := c.Discover(ctx, 2500*time.Millisecond); return err },
			want:  []string{"discover", "--timeout", "2.5"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeUV(t, c.reply, 0)
			if err := c.call(f.client); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got := f.cliArgs(t); !slices.Equal(got, c.want) {
				t.Fatalf("cli.py argv = %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestRunDecodesResults(t *testing.T) {
	ctx := context.Background()

	f := newFakeUV(t, `{"ok":true,"data":{"address":"10.0.0.2","login":"deck","key_path":"/k","strict_host_keys":true}}`, 0)
	info, err := f.client.ConnectionInfo(ctx, "deck.local", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Address != "10.0.0.2" || info.KeyPath != "/k" || !info.StrictHostKeys {
		t.Errorf("ConnectionInfo = %+v", info)
	}

	f = newFakeUV(t, `{"ok":true,"data":{"stdout":"out","stderr":"err","exit_status":3}}`, 0)
	res, err := f.client.RunCommand(ctx, "deck.local", "", "false")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "out" || res.Stderr != "err" || res.ExitStatus != 3 {
		t.Errorf("RunCommand = %+v", res)
	}

	f = newFakeUV(t, `{"ok":true,"data":[{"name":"deck","address":"10.0.0.2","port":32000}]}`, 0)
	found, err := f.client.Discover(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Port != 32000 {
		t.Errorf("Discover = %+v", found)
	}

	f = newFakeUV(t, `{"ok":true,"data":{"battery":50}}`, 0)
	st, err := f.client.Status(ctx, "deck.local", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Raw["battery"] != float64(50) {
		t.Errorf("Status = %+v", st.Raw)
	}
}

func TestRunWrongDataShapeIsAnError(t *testing.T) {
	ctx := context.Background()
	f := newFakeUV(t, `{"ok":true,"data":"not an object"}`, 0)
	if _, err := f.client.Status(ctx, "deck.local", ""); err == nil {
		t.Error("Status: expected a decode error")
	}
	if _, err := f.client.ListGames(ctx, "deck.local", ""); err == nil {
		t.Error("ListGames: expected a decode error")
	}
	if _, err := f.client.ConnectionInfo(ctx, "deck.local", ""); err == nil {
		t.Error("ConnectionInfo: expected a decode error")
	}
	if _, err := f.client.RunCommand(ctx, "deck.local", "", "x"); err == nil {
		t.Error("RunCommand: expected a decode error")
	}
}

func TestRunSurfacesCLIErrors(t *testing.T) {
	f := newFakeUV(t, `{"ok":false,"error":"no route to host","error_kind":"unreachable"}`, 1)
	err := f.client.Deploy(context.Background(), "deck.local", "", "game", "/b", false, nil)
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Kind != "unreachable" || cliErr.Message != "no route to host" {
		t.Fatalf("err = %#v, want CLIError unreachable", err)
	}
}

func TestRunFailureWithoutEnvelope(t *testing.T) {
	f := newFakeUV(t, "", 2)
	err := f.client.Register(context.Background(), "deck.local")
	var cliErr *CLIError
	if err == nil || errors.As(err, &cliErr) || !strings.Contains(err.Error(), "uv run failed") {
		t.Fatalf("err = %v, want a plain uv run failure", err)
	}
}

func TestRunEnvironment(t *testing.T) {
	t.Run("defaults UV_PROJECT_ENVIRONMENT into the user cache", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", t.TempDir()) // honored on Linux; macOS uses ~/Library/Caches
		t.Setenv("UV_PROJECT_ENVIRONMENT", "")
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Skipf("no user cache dir: %v", err)
		}
		f := newFakeUV(t, okNull, 0)
		if err := f.client.Register(context.Background(), "d"); err != nil {
			t.Fatal(err)
		}
		lines := readLines(t, f.envFile)
		// $PWD is the resolved path (macOS temp dirs live under the
		// /var -> /private/var symlink), so compare resolved paths.
		wantDir, err := filepath.EvalSymlinks(f.client.PythonDir)
		if err != nil {
			t.Fatal(err)
		}
		if gotDir, _ := filepath.EvalSymlinks(lines[0]); gotDir != wantDir {
			t.Errorf("working dir = %q, want %q", lines[0], wantDir)
		}
		if want := filepath.Join(cache, "lazydeck", "python"); lines[1] != want {
			t.Errorf("UV_PROJECT_ENVIRONMENT = %q, want %q", lines[1], want)
		}
	})
	t.Run("respects a caller-set UV_PROJECT_ENVIRONMENT", func(t *testing.T) {
		t.Setenv("UV_PROJECT_ENVIRONMENT", "/custom/venv")
		f := newFakeUV(t, okNull, 0)
		if err := f.client.Register(context.Background(), "d"); err != nil {
			t.Fatal(err)
		}
		if got := readLines(t, f.envFile)[1]; got != "/custom/venv" {
			t.Errorf("UV_PROJECT_ENVIRONMENT = %q, want /custom/venv", got)
		}
	})
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(bytes.TrimRight(data, "\n")), "\n")
	for len(lines) < 2 {
		lines = append(lines, "")
	}
	return lines
}

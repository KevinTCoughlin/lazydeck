package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
	"github.com/kevintcoughlin/lazydeck/internal/jobs"
	"github.com/kevintcoughlin/lazydeck/internal/notify"
)

var deck1 = config.Device{Name: "deck-1", Machine: "steamdeck.local", Login: "deck"}

func TestStatusForKind(t *testing.T) {
	cases := map[string]int{
		"unauthorized":    http.StatusUnauthorized,
		"forbidden":       http.StatusForbidden,
		"invalid-input":   http.StatusBadRequest,
		"invalid-request": http.StatusBadRequest,
		"not-found":       http.StatusNotFound,
		"device-busy":     http.StatusConflict,
		"unreachable":     http.StatusBadGateway,
		"auth-failed":     http.StatusBadGateway,
		"script-error":    http.StatusInternalServerError,
		"unknown":         http.StatusInternalServerError,
		"something-new":   http.StatusInternalServerError,
	}
	for kind, want := range cases {
		if got := statusForKind(kind); got != want {
			t.Errorf("statusForKind(%q) = %d, want %d", kind, got, want)
		}
	}
}

func TestGamesListsDeviceGames(t *testing.T) {
	fc := &fakeClient{games: []any{map[string]any{"gameid": "mygame"}}}
	_, ts := newTestServer(fc, deck1)
	defer ts.Close()

	resp := doRequest(t, ts, "GET", "/v1/devices/deck-1/games", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody[struct {
		Games []map[string]any `json:"games"`
	}](t, resp)
	if len(body.Games) != 1 || body.Games[0]["gameid"] != "mygame" {
		t.Fatalf("games = %#v", body.Games)
	}
}

// An error that isn't a *client.CLIError can carry raw subprocess output
// (hostnames, SSH paths). It must reach the caller only as a generic
// "unknown" error, never echoed back.
func TestUnclassifiedClientErrorIsNotEchoed(t *testing.T) {
	secret := "ssh: /home/someone/.ssh/id_devkit: permission denied on 10.0.0.42"
	cases := []struct {
		name   string
		fc     *fakeClient
		method string
		path   string
		body   any
	}{
		{"games", &fakeClient{gamesErr: errors.New(secret)}, "GET", "/v1/devices/deck-1/games", nil},
		{"status", &fakeClient{statusErr: errors.New(secret)}, "GET", "/v1/devices/deck-1/status", nil},
		{"pair", &fakeClient{registerErr: errors.New(secret)}, "POST", "/v1/devices/deck-1/pair", map[string]any{}},
		{"discover", &fakeClient{discoverErr: errors.New(secret)}, "POST", "/v1/devices/discover", map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ts := newTestServer(c.fc, deck1)
			defer ts.Close()
			resp := doRequest(t, ts, c.method, c.path, c.body, testToken)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", resp.StatusCode)
			}
			body := decodeBody[errorEnvelope](t, resp)
			if body.Error.Kind != "unknown" {
				t.Errorf("kind = %q, want unknown", body.Error.Kind)
			}
			if strings.Contains(body.Error.Message, "10.0.0.42") || strings.Contains(body.Error.Message, ".ssh") {
				t.Errorf("response leaks the underlying error: %q", body.Error.Message)
			}
		})
	}
}

func TestPair(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		_, ts := newTestServer(&fakeClient{}, deck1)
		defer ts.Close()
		resp := doRequest(t, ts, "POST", "/v1/devices/deck-1/pair", map[string]any{}, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeBody[struct {
			ID     string `json:"id"`
			Paired bool   `json:"paired"`
		}](t, resp)
		if body.ID != "deck-1" || !body.Paired {
			t.Fatalf("body = %+v", body)
		}
	})
	t.Run("devkit auth failure maps to 502", func(t *testing.T) {
		fc := &fakeClient{registerErr: &client.CLIError{Kind: "auth-failed", Message: "pairing rejected on device"}}
		_, ts := newTestServer(fc, deck1)
		defer ts.Close()
		resp := doRequest(t, ts, "POST", "/v1/devices/deck-1/pair", map[string]any{}, testToken)
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", resp.StatusCode)
		}
		if body := decodeBody[errorEnvelope](t, resp); body.Error.Kind != "auth-failed" {
			t.Fatalf("kind = %q, want auth-failed", body.Error.Kind)
		}
	})
	t.Run("unknown device", func(t *testing.T) {
		_, ts := newTestServer(&fakeClient{}, deck1)
		defer ts.Close()
		resp := doRequest(t, ts, "POST", "/v1/devices/nope/pair", map[string]any{}, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestDiscover(t *testing.T) {
	fc := &fakeClient{discoverResult: []client.DiscoveredDevice{{}}}
	_, ts := newTestServer(fc)
	defer ts.Close()

	for _, body := range []any{map[string]any{}, map[string]any{"timeout_seconds": 0.5}} {
		resp := doRequest(t, ts, "POST", "/v1/devices/discover", body, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("body %v: status = %d, want 200", body, resp.StatusCode)
		}
		got := decodeBody[struct {
			Devices []client.DiscoveredDevice `json:"devices"`
		}](t, resp)
		if len(got.Devices) != 1 {
			t.Fatalf("body %v: devices = %#v", body, got.Devices)
		}
	}

	resp := doRequest(t, ts, "POST", "/v1/devices/discover", map[string]any{"timeout_seconds": -1}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative timeout: status = %d, want 400", resp.StatusCode)
	}
	resp = doRequest(t, ts, "POST", "/v1/devices/discover", map[string]any{"timeout_seconds": "soon"}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong-typed timeout: status = %d, want 400", resp.StatusCode)
	}
}

func TestReadConnectionInfo(t *testing.T) {
	dir := t.TempDir()

	if _, err := ReadConnectionInfo(filepath.Join(dir, "missing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v, want ErrNotExist", err)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConnectionInfo(bad); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Errorf("malformed file: err = %v, want a parse error", err)
	}

	good := filepath.Join(dir, "serve.json")
	if err := os.WriteFile(good, []byte(`{"pid":7,"port":8123,"base_url":"http://127.0.0.1:8123","token":"t","api_version":"v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := ReadConnectionInfo(good)
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != 7 || info.Port != 8123 || info.Token != "t" || info.APIVersion != "v1" {
		t.Fatalf("info = %+v", info)
	}
}

func TestConnectionFilePathFallsBackToCacheDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir on this platform: %v", err)
	}
	path, err := ConnectionFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cache, "lazydeck", "serve.json"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

type chanSender chan notify.Event

func (c chanSender) Send(ctx context.Context, ev notify.Event) error {
	c <- ev
	return nil
}

func TestNotifyJobComplete(t *testing.T) {
	finished := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		snap     jobs.Snapshot
		wantSent bool
		want     notify.Event
	}{
		{
			name:     "succeeded",
			snap:     jobs.Snapshot{DeviceID: "deck-1", Operation: "deploy", Status: jobs.Succeeded, FinishedAt: &finished, LastMessage: "done"},
			wantSent: true,
			want:     notify.Event{DeviceID: "deck-1", Operation: "deploy", Succeeded: true, Message: "done", Time: finished},
		},
		{
			name:     "failed uses the error message",
			snap:     jobs.Snapshot{DeviceID: "deck-1", Operation: "deploy", Status: jobs.Failed, FinishedAt: &finished, LastMessage: "step 3", Error: &jobs.Error{Kind: "script-error", Message: "rsync failed"}},
			wantSent: true,
			want:     notify.Event{DeviceID: "deck-1", Operation: "deploy", Succeeded: false, Message: "rsync failed", Time: finished},
		},
		{name: "cancelled is not notified", snap: jobs.Snapshot{Status: jobs.Cancelled, FinishedAt: &finished}},
		{name: "running is not notified", snap: jobs.Snapshot{Status: jobs.Status("running")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := make(chanSender, 1)
			notifyJobComplete(notify.Fanout{ch}, c.snap)
			if !c.wantSent {
				// notifyJobComplete returns before spawning its sender
				// goroutine for these statuses, so nothing can arrive later.
				select {
				case ev := <-ch:
					t.Fatalf("unexpected notification %+v", ev)
				default:
				}
				return
			}
			select {
			case ev := <-ch:
				if ev != c.want {
					t.Fatalf("event = %+v, want %+v", ev, c.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no notification sent")
			}
		})
	}
}

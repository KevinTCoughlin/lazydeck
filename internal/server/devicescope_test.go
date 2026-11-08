package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/apikey"
	"github.com/kevintcoughlin/lazydeck/internal/audit"
	"github.com/kevintcoughlin/lazydeck/internal/config"
)

func newTwoDeviceFixture(t *testing.T) *authFixture {
	t.Helper()
	keysPath := filepath.Join(t.TempDir(), "api-keys.json")
	s, err := New(&fakeClient{deployDelay: time.Second}, &config.Config{Devices: []config.Device{
		{Name: "lab-1", Machine: "10.0.0.1"},
		{Name: "lab-2", Machine: "10.0.0.2"},
	}}, testToken)
	if err != nil {
		t.Fatal(err)
	}
	buf := &syncBuffer{}
	s.keys = apikey.NewStore(keysPath)
	s.audit = audit.New(buf)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &authFixture{srv: s, ts: ts, keysPath: keysPath, log: buf}
}

func (f *authFixture) deviceKey(t *testing.T, devices []string, scopes ...apikey.Scope) string {
	t.Helper()
	token, _, err := apikey.CreateForDevices(f.keysPath, "qa", scopes, devices, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestDeviceScopedKeyListsOnlyItsDevices(t *testing.T) {
	f := newTwoDeviceFixture(t)
	token := f.deviceKey(t, []string{"lab-2"}, apikey.ScopeRead)
	resp := doRequest(t, f.ts, http.MethodGet, "/v1/devices", nil, token)
	body := decodeBody[struct {
		Devices []deviceResponse `json:"devices"`
	}](t, resp)
	if len(body.Devices) != 1 || body.Devices[0].ID != "lab-2" {
		t.Fatalf("devices = %+v, want only lab-2", body.Devices)
	}

	// An unrestricted key and the session token still see both.
	for _, tok := range []string{f.deviceKey(t, nil, apikey.ScopeRead), testToken} {
		resp := doRequest(t, f.ts, http.MethodGet, "/v1/devices", nil, tok)
		all := decodeBody[struct {
			Devices []deviceResponse `json:"devices"`
		}](t, resp)
		if len(all.Devices) != 2 {
			t.Fatalf("unrestricted devices = %+v", all.Devices)
		}
	}
}

func TestDeviceScopedKeyIsDeniedOtherDevices(t *testing.T) {
	f := newTwoDeviceFixture(t)
	f.srv.client.(*fakeClient).deployDelay = 0
	token := f.deviceKey(t, []string{"lab-1"}, apikey.ScopeRead, apikey.ScopeDeploy)

	deploy := map[string]any{"game_id": "game", "directory": "/srv/build"}
	resp := doRequest(t, f.ts, http.MethodPost, "/v1/devices/lab-2/deployments", deploy, token)
	body := decodeBody[errorEnvelope](t, resp)
	if resp.StatusCode != http.StatusForbidden || body.Error.Kind != "forbidden" {
		t.Fatalf("deploy to lab-2 = %d %+v, want 403", resp.StatusCode, body)
	}
	e, ok := findEntry(f.log.entries(t), "deploy", audit.ResultDenied)
	if !ok || e.Device != "lab-2" || e.Reason != "key is limited to other devices" {
		t.Fatalf("denied entry = %+v (found %v)", e, ok)
	}

	// A device that doesn't exist is also 403, not 404: a scoped key
	// can't probe which device names exist.
	resp = doRequest(t, f.ts, http.MethodGet, "/v1/devices/ghost/status", nil, token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status of unknown device = %d, want 403", resp.StatusCode)
	}

	resp = doRequest(t, f.ts, http.MethodPost, "/v1/devices/lab-1/deployments", deploy, token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("deploy to lab-1 = %d, want 202", resp.StatusCode)
	}
}

func TestDeviceScopedKeyCannotSeeOtherDevicesJobs(t *testing.T) {
	f := newTwoDeviceFixture(t)
	resp := doRequest(t, f.ts, http.MethodPost, "/v1/devices/lab-2/deployments",
		map[string]any{"game_id": "game", "directory": "/srv/build"}, testToken)
	accepted := decodeBody[struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}](t, resp)
	if resp.StatusCode != http.StatusAccepted || accepted.Job.ID == "" {
		t.Fatalf("session deploy = %d %+v", resp.StatusCode, accepted)
	}

	scoped := f.deviceKey(t, []string{"lab-1"}, apikey.ScopeRead, apikey.ScopeJobsCancel)
	for _, path := range []string{"/v1/jobs/" + accepted.Job.ID, "/v1/jobs/" + accepted.Job.ID + "/events"} {
		resp := doRequest(t, f.ts, http.MethodGet, path, nil, scoped)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s with a lab-1 key = %d, want 404", path, resp.StatusCode)
		}
	}
	resp = doRequest(t, f.ts, http.MethodDelete, "/v1/jobs/"+accepted.Job.ID, nil, scoped)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel with a lab-1 key = %d, want 404", resp.StatusCode)
	}

	unscoped := f.deviceKey(t, nil, apikey.ScopeRead)
	resp = doRequest(t, f.ts, http.MethodGet, "/v1/jobs/"+accepted.Job.ID, nil, unscoped)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET job with an unrestricted key = %d, want 200", resp.StatusCode)
	}
}

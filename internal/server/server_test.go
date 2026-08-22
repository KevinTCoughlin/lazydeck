package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
)

// fakeClient implements devkitClient in-memory so these tests exercise
// routing, auth, and error mapping without shelling out to a real `uv run`.
type fakeClient struct {
	mu sync.Mutex

	discoverResult []client.DiscoveredDevice
	discoverErr    error
	registerErr    error
	status         *client.Status
	statusErr      error
	games          []any
	gamesErr       error
	deployErr      error
	deployDelay    time.Duration
	syncLogsErr    error

	deployCalls int
}

func (f *fakeClient) Discover(ctx context.Context, timeout time.Duration) ([]client.DiscoveredDevice, error) {
	return f.discoverResult, f.discoverErr
}

func (f *fakeClient) Register(ctx context.Context, machine string) error { return f.registerErr }

func (f *fakeClient) Status(ctx context.Context, machine, login string) (*client.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeClient) ListGames(ctx context.Context, machine, login string) ([]any, error) {
	return f.games, f.gamesErr
}

func (f *fakeClient) Deploy(ctx context.Context, machine, login, gameID, directory string, deleteExtraneous bool) error {
	f.mu.Lock()
	f.deployCalls++
	f.mu.Unlock()
	if f.deployDelay > 0 {
		select {
		case <-time.After(f.deployDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.deployErr
}

func (f *fakeClient) SyncLogs(ctx context.Context, machine, login, gameID, directory string) error {
	return f.syncLogsErr
}

const testToken = "test-token"

func newTestServer(fc *fakeClient, devices ...config.Device) (*Server, *httptest.Server) {
	s, err := New(fc, &config.Config{Devices: devices}, testToken)
	if err != nil {
		panic(err) // test helper: a bad fixture here is a test bug, not a runtime condition to assert on
	}
	ts := httptest.NewServer(s.Handler())
	return s, ts
}

func doRequest(t *testing.T, ts *httptest.Server, method, path string, body any, token string) *http.Response {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = strings.NewReader(string(data))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func decodeBody[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return v
}

func TestHealthRequiresAuth(t *testing.T) {
	_, ts := newTestServer(&fakeClient{})
	defer ts.Close()

	if resp := doRequest(t, ts, "GET", "/v1/health", nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", resp.StatusCode)
	}
	if resp := doRequest(t, ts, "GET", "/v1/health", nil, "wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", resp.StatusCode)
	}
	resp := doRequest(t, ts, "GET", "/v1/health", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct token: status = %d, want 200", resp.StatusCode)
	}
}

func TestCapabilitiesReportsLaunchStopUnsupported(t *testing.T) {
	_, ts := newTestServer(&fakeClient{})
	defer ts.Close()

	resp := doRequest(t, ts, "GET", "/v1/capabilities", nil, testToken)
	body := decodeBody[capabilitiesResponse](t, resp)
	if body.Operations["launch"] || body.Operations["stop"] {
		t.Fatalf("capabilities = %#v, want launch and stop false", body.Operations)
	}
	if !body.Operations["deploy"] {
		t.Fatalf("capabilities = %#v, want deploy true", body.Operations)
	}
}

func TestListDevicesReflectsConfig(t *testing.T) {
	_, ts := newTestServer(&fakeClient{}, config.Device{Name: "deck-1", Machine: "steamdeck.local", Login: "deck"})
	defer ts.Close()

	resp := doRequest(t, ts, "GET", "/v1/devices", nil, testToken)
	body := decodeBody[struct {
		Devices []deviceResponse `json:"devices"`
	}](t, resp)
	if len(body.Devices) != 1 || body.Devices[0].ID != "deck-1" || body.Devices[0].Machine != "steamdeck.local" {
		t.Fatalf("devices = %#v", body.Devices)
	}
}

func TestUnknownDeviceIs404(t *testing.T) {
	_, ts := newTestServer(&fakeClient{})
	defer ts.Close()

	resp := doRequest(t, ts, "GET", "/v1/devices/nope/status", nil, testToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := decodeBody[errorEnvelope](t, resp)
	if body.Error.Kind != "not-found" {
		t.Fatalf("kind = %q, want not-found", body.Error.Kind)
	}
}

func TestStatusMapsCLIErrorKindToStatus(t *testing.T) {
	fc := &fakeClient{statusErr: &client.CLIError{Kind: "unreachable", Message: "no route to host"}}
	_, ts := newTestServer(fc, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	resp := doRequest(t, ts, "GET", "/v1/devices/deck-1/status", nil, testToken)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body := decodeBody[errorEnvelope](t, resp)
	if body.Error.Kind != "unreachable" || body.Error.Message != "no route to host" {
		t.Fatalf("error = %#v", body.Error)
	}
}

func TestDeployRequiresGameIDAndDirectory(t *testing.T) {
	_, ts := newTestServer(&fakeClient{}, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	resp := doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments", map[string]string{}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestDeployRunsAsJobAndReportsSuccess(t *testing.T) {
	fc := &fakeClient{}
	_, ts := newTestServer(fc, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	resp := doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments",
		map[string]string{"game_id": "mygame", "directory": "/tmp/build"}, testToken)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	created := decodeBody[struct {
		Job struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"job"`
	}](t, resp)
	if created.Job.ID == "" {
		t.Fatal("expected a job id")
	}

	deadline := time.Now().Add(time.Second)
	for {
		resp := doRequest(t, ts, "GET", "/v1/jobs/"+created.Job.ID, nil, testToken)
		got := decodeBody[struct {
			Job struct {
				Status string `json:"status"`
			} `json:"job"`
		}](t, resp)
		if got.Job.Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not succeed in time, last status %q", got.Job.Status)
		}
		time.Sleep(time.Millisecond)
	}

	if fc.deployCalls != 1 {
		t.Fatalf("deployCalls = %d, want 1", fc.deployCalls)
	}
}

func TestSecondDeployToSameDeviceIsBusy(t *testing.T) {
	fc := &fakeClient{deployDelay: 200 * time.Millisecond}
	_, ts := newTestServer(fc, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	first := doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments",
		map[string]string{"game_id": "mygame", "directory": "/tmp/build"}, testToken)
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first deploy status = %d, want 202", first.StatusCode)
	}

	second := doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments",
		map[string]string{"game_id": "mygame", "directory": "/tmp/build"}, testToken)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second deploy status = %d, want 409", second.StatusCode)
	}
}

func TestLaunchAndStopAreNotImplemented(t *testing.T) {
	_, ts := newTestServer(&fakeClient{}, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	for _, path := range []string{
		"/v1/devices/deck-1/games/mygame/launch",
		"/v1/devices/deck-1/games/mygame/stop",
	} {
		resp := doRequest(t, ts, "POST", path, nil, testToken)
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s status = %d, want 501", path, resp.StatusCode)
		}
	}
}

func TestJobEventsStreamsUntilTerminal(t *testing.T) {
	fc := &fakeClient{}
	_, ts := newTestServer(fc, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	created := decodeBody[struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}](t, doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments",
		map[string]string{"game_id": "mygame", "directory": "/tmp/build"}, testToken))

	// A context deadline (rather than only a deadline variable checked
	// between iterations) bounds this test even if the server never emits
	// a terminal event at all: scanner.Scan() blocks on a body read with
	// nothing else to make it return, so without this the test would hang
	// until the whole suite's global timeout instead of failing cleanly.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/v1/jobs/"+created.Job.ID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	scanner := bufio.NewScanner(resp.Body)
	sawTerminal := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			var evt struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
				t.Fatalf("unmarshal event: %v", err)
			}
			if evt.Status == "succeeded" {
				sawTerminal = true
				break
			}
		}
	}
	if !sawTerminal {
		t.Fatal("stream ended without observing a terminal event (or the 5s context deadline cut the read off first)")
	}
}

func TestCancelJobEndpoint(t *testing.T) {
	fc := &fakeClient{deployDelay: time.Second}
	_, ts := newTestServer(fc, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	created := decodeBody[struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}](t, doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments",
		map[string]string{"game_id": "mygame", "directory": "/tmp/build"}, testToken))

	resp := doRequest(t, ts, "DELETE", "/v1/jobs/"+created.Job.ID, nil, testToken)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got := decodeBody[struct {
			Job struct {
				Status string `json:"status"`
			} `json:"job"`
		}](t, doRequest(t, ts, "GET", "/v1/jobs/"+created.Job.ID, nil, testToken))
		if got.Job.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not cancel in time, last status %q", got.Job.Status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNewRejectsDuplicateDeviceNames(t *testing.T) {
	cfg := &config.Config{Devices: []config.Device{
		{Name: "deck-1", Machine: "a.local"},
		{Name: "deck-1", Machine: "b.local"},
	}}
	if _, err := New(&fakeClient{}, cfg, testToken); err == nil {
		t.Fatal("expected an error for duplicate device names")
	}
}

func TestUnmatchedRouteReturnsJSONNotFound(t *testing.T) {
	_, ts := newTestServer(&fakeClient{})
	defer ts.Close()

	resp := doRequest(t, ts, "GET", "/v1/nope", nil, testToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	body := decodeBody[errorEnvelope](t, resp)
	if body.Error.Kind != "not-found" {
		t.Fatalf("kind = %q, want not-found", body.Error.Kind)
	}
}

func TestDisallowedMethodReturnsJSONError(t *testing.T) {
	_, ts := newTestServer(&fakeClient{})
	defer ts.Close()

	resp := doRequest(t, ts, "DELETE", "/v1/health", nil, testToken)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	body := decodeBody[errorEnvelope](t, resp)
	if body.Error.Kind != "invalid-input" {
		t.Fatalf("kind = %q, want invalid-input", body.Error.Kind)
	}
}

func TestDeployRejectsRelativeDirectory(t *testing.T) {
	_, ts := newTestServer(&fakeClient{}, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	resp := doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments",
		map[string]string{"game_id": "mygame", "directory": "build/output"}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a relative directory", resp.StatusCode)
	}
}

func TestDeployRejectsInvalidGameID(t *testing.T) {
	_, ts := newTestServer(&fakeClient{}, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	for _, gameID := range []string{
		"-leading-dash",
		"has spaces",
		"has/slash",
		string(make([]byte, 300)), // over any sane length limit
	} {
		resp := doRequest(t, ts, "POST", "/v1/devices/deck-1/deployments",
			map[string]string{"game_id": gameID, "directory": "/tmp/build"}, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("game_id %q: status = %d, want 400", gameID, resp.StatusCode)
		}
	}
}

func TestDiscoverRejectsOutOfRangeTimeout(t *testing.T) {
	_, ts := newTestServer(&fakeClient{})
	defer ts.Close()

	for _, seconds := range []float64{-1, 1e9} {
		resp := doRequest(t, ts, "POST", "/v1/devices/discover", map[string]float64{"timeout_seconds": seconds}, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("timeout_seconds=%v: status = %d, want 400", seconds, resp.StatusCode)
		}
	}
}

func TestDecodeJSONBodyRejectsTrailingData(t *testing.T) {
	_, ts := newTestServer(&fakeClient{}, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/devices/deck-1/deployments",
		strings.NewReader(`{"game_id":"mygame","directory":"/tmp/build"}{"extra":true}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a body with trailing JSON", resp.StatusCode)
	}
}

func TestDecodeJSONBodyRejectsOversizedRequest(t *testing.T) {
	_, ts := newTestServer(&fakeClient{}, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	huge := strings.Repeat("a", maxRequestBodyBytes+1)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/devices/deck-1/deployments",
		strings.NewReader(`{"game_id":"mygame","directory":"/tmp/build","note":"`+huge+`"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an oversized body", resp.StatusCode)
	}
}

func TestLogsSyncGameIDIsOptional(t *testing.T) {
	fc := &fakeClient{}
	_, ts := newTestServer(fc, config.Device{Name: "deck-1", Machine: "steamdeck.local"})
	defer ts.Close()

	resp := doRequest(t, ts, "POST", "/v1/devices/deck-1/logs/sync",
		map[string]string{"directory": "/tmp/logs"}, testToken)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 with no game_id, since sync_logs always fetches the full log/minidump directory", resp.StatusCode)
	}
}

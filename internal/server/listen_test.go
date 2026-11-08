package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/apikey"
	"github.com/kevintcoughlin/lazydeck/internal/audit"
	"github.com/kevintcoughlin/lazydeck/internal/config"
)

func TestCheckListenRequiresAnActiveAPIKey(t *testing.T) {
	now := time.Now()
	keysPath := filepath.Join(t.TempDir(), "api-keys.json")

	if _, err := checkListen("0.0.0.0:47600", "", now); err == nil || !strings.Contains(err.Error(), "API keys are disabled") {
		t.Fatalf("no key store: %v", err)
	}
	if _, err := checkListen("0.0.0.0:47600", keysPath, now); err == nil || !strings.Contains(err.Error(), "no active API key") {
		t.Fatalf("empty key store: %v", err)
	}

	_, key, err := apikey.Create(keysPath, "old", []apikey.Scope{apikey.ScopeRead}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := apikey.Revoke(keysPath, key.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := apikey.Create(keysPath, "short", []apikey.Scope{apikey.ScopeRead}, time.Minute, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := checkListen("0.0.0.0:47600", keysPath, now); err == nil || !strings.Contains(err.Error(), "no active API key") {
		t.Fatalf("only revoked/expired keys: %v", err)
	}

	if err := os.WriteFile(keysPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkListen("0.0.0.0:47600", keysPath, now); err == nil || !strings.Contains(err.Error(), "refusing to start --listen") {
		t.Fatalf("unreadable key store: %v", err)
	}
}

func TestCheckListenAddresses(t *testing.T) {
	now := time.Now()
	keysPath := filepath.Join(t.TempDir(), "api-keys.json")
	if _, _, err := apikey.Create(keysPath, "ci", []apikey.Scope{apikey.ScopeRead}, 0, now); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		addr     string
		loopback bool
		wantErr  string
	}{
		{"0.0.0.0:47600", false, ""},
		{":47600", false, ""},
		{"[::]:47600", false, ""},
		{"192.168.10.5:47600", false, ""},
		{"127.0.0.1:47601", true, ""},
		{"[::1]:47601", true, ""},
		{"47600", false, "want HOST:PORT"},
		{"0.0.0.0:0", false, "port must be"},
		{"0.0.0.0:http", false, "port must be"},
		{"devbox.local:47600", false, "not a hostname"},
	}
	for _, tc := range cases {
		loopback, err := checkListen(tc.addr, keysPath, now)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("checkListen(%q) = %v, want error containing %q", tc.addr, err, tc.wantErr)
			}
			continue
		}
		if err != nil || loopback != tc.loopback {
			t.Errorf("checkListen(%q) = %v, %v; want loopback=%v", tc.addr, loopback, err, tc.loopback)
		}
	}
}

// Run must refuse --listen without an API key before it creates a
// connection file, opens the audit log, or binds a socket.
func TestRunRefusesListenWithoutAuth(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	err := Run(context.Background(), &fakeClient{}, &config.Config{}, Options{
		Listen:       "0.0.0.0:47600",
		APIKeysPath:  filepath.Join(t.TempDir(), "api-keys.json"),
		AuditLogPath: auditPath,
	})
	if err == nil || !strings.Contains(err.Error(), "no active API key") {
		t.Fatalf("Run = %v, want refusal", err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "lazydeck", "serve.json")); !os.IsNotExist(err) {
		t.Fatalf("connection file exists after refusal: %v", err)
	}
	if _, err := os.Stat(auditPath); !os.IsNotExist(err) {
		t.Fatalf("audit log created after refusal: %v", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func get(t *testing.T, url, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// End to end through Run: the --listen listener (on a loopback address so
// the test stays local) takes API keys but not the session token, while
// the loopback listener in the connection file still takes both.
func TestRunListenAcceptsAPIKeysOnly(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	keysPath := filepath.Join(t.TempDir(), "api-keys.json")
	ciKey, _, err := apikey.Create(keysPath, "ci", []apikey.Scope{apikey.ScopeRead}, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	listenAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, &fakeClient{}, &config.Config{Devices: []config.Device{{Name: "deck", Machine: "m"}}}, Options{
			Listen:       listenAddr,
			APIKeysPath:  keysPath,
			AuditLogPath: filepath.Join(t.TempDir(), "audit.log"),
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})

	connPath, err := connectionFilePath()
	if err != nil {
		t.Fatal(err)
	}
	var info ConnectionInfo
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(connPath)
		if err == nil && json.Unmarshal(data, &info) == nil && info.Token != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection file never appeared: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(info.BaseURL, "http://127.0.0.1:") {
		t.Fatalf("connection file BaseURL = %s, want the loopback listener", info.BaseURL)
	}

	netURL := "http://" + listenAddr + "/v1/devices"
	if got := get(t, netURL, ciKey); got != http.StatusOK {
		t.Fatalf("API key on --listen = %d, want 200", got)
	}
	if got := get(t, netURL, info.Token); got != http.StatusUnauthorized {
		t.Fatalf("session token on --listen = %d, want 401", got)
	}
	if got := get(t, info.BaseURL+"/v1/devices", info.Token); got != http.StatusOK {
		t.Fatalf("session token on loopback = %d, want 200", got)
	}
	if got := get(t, info.BaseURL+"/v1/devices", ciKey); got != http.StatusOK {
		t.Fatalf("API key on loopback = %d, want 200", got)
	}
}

func TestNetworkHandlerRefusalIsAudited(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{})
	ts := httptest.NewServer(f.srv.NetworkHandler())
	t.Cleanup(ts.Close)
	resp := doRequest(t, ts, http.MethodGet, "/v1/devices", nil, testToken)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session token on network handler = %d, want 401", resp.StatusCode)
	}
	e, ok := findEntry(f.log.entries(t), "auth", audit.ResultUnauthorized)
	if !ok || !strings.Contains(e.Reason, "network listener") {
		t.Fatalf("audit entry = %+v (found %v)", e, ok)
	}
	if strings.Contains(f.log.String(), testToken) {
		t.Fatal("audit log contains the session token")
	}
}

func TestListenWarningIsLoud(t *testing.T) {
	w := listenWarning("0.0.0.0:47600")
	for _, want := range []string{"WARNING", "plain HTTP", "API keys only", "trusted network"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning missing %q:\n%s", want, w)
		}
	}
}

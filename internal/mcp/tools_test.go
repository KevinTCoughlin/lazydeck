package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kevintcoughlin/lazydeck/internal/mcpapi"
)

// newFakeAPIServer stands in for `lazydeck serve` at the wire level, the
// same way internal/mcpapi's own tests do, so registerTools can be
// exercised end-to-end (real MCP tool-call plumbing, real JSON over an
// httptest.Server) without a devkit or a subprocess.
func newFakeAPIServer(t *testing.T, handler http.HandlerFunc) *mcpapi.Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return mcpapi.New(ts.URL, "unused-in-tests")
}

// connectedClient builds an mcp.Server via registerTools, connects it to a
// client over an in-memory transport, and returns the live client session
// for the test to call tools against.
func connectedClient(t *testing.T, cli *mcpapi.Client, allowMutations bool) *sdkmcp.ClientSession {
	t.Helper()
	ctx := t.Context()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "lazydeck-test"}, nil)
	registerTools(server, cli, allowMutations)

	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolNames(t *testing.T, session *sdkmcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestRegisterTools_ReadOnlyByDefault(t *testing.T) {
	cli := newFakeAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request in a tool-registration-only test: %s %s", r.Method, r.URL.Path)
	})
	session := connectedClient(t, cli, false)

	names := toolNames(t, session)
	for _, want := range []string{"health", "get_capabilities", "list_devices", "discover_devices", "device_status", "list_games", "get_job"} {
		if !names[want] {
			t.Errorf("read-only tool %q missing", want)
		}
	}
	for _, mutating := range []string{"deploy", "sync_logs", "pair_device", "cancel_job", "launch_game", "stop_game"} {
		if names[mutating] {
			t.Errorf("mutating tool %q registered without --allow-mutations", mutating)
		}
	}
}

func TestRegisterTools_MutationsOptIn(t *testing.T) {
	cli := newFakeAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request in a tool-registration-only test: %s %s", r.Method, r.URL.Path)
	})
	session := connectedClient(t, cli, true)

	names := toolNames(t, session)
	for _, want := range []string{"deploy", "sync_logs", "pair_device", "cancel_job", "launch_game", "stop_game"} {
		if !names[want] {
			t.Errorf("mutating tool %q missing with --allow-mutations", want)
		}
	}
}

func TestListDevicesTool_EndToEnd(t *testing.T) {
	cli := newFakeAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/devices" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"devices": []mcpapi.Device{{ID: "deck-1", Machine: "10.0.0.5"}},
		})
	})
	session := connectedClient(t, cli, false)

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "list_devices"})
	if err != nil {
		t.Fatalf("CallTool(list_devices): %v", err)
	}
	if res.IsError {
		t.Fatalf("list_devices returned IsError: %+v", res.Content)
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("res.Content[0] = %T, want *TextContent", res.Content[0])
	}
	if !strings.Contains(text.Text, "deck-1") {
		t.Errorf("result %q does not mention deck-1", text.Text)
	}
}

// TestDiscoverDevicesTool_ClampsTimeout locks in that an over-large
// timeout_seconds is clamped to the API's own documented max (300s, per
// api/openapi.yaml and internal/server/handlers.go's
// maxDiscoverTimeoutSeconds) rather than forwarded as-is, since the server
// would otherwise reject it outright with a 400.
func TestDiscoverDevicesTool_ClampsTimeout(t *testing.T) {
	var gotBody struct {
		TimeoutSeconds float64 `json:"timeout_seconds"`
	}
	cli := newFakeAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"devices": []map[string]any{}})
	})
	session := connectedClient(t, cli, false)

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name:      "discover_devices",
		Arguments: map[string]any{"timeout_seconds": 10000.0},
	})
	if err != nil {
		t.Fatalf("CallTool(discover_devices): %v", err)
	}
	if res.IsError {
		t.Fatalf("discover_devices returned IsError: %+v", res.Content)
	}
	if gotBody.TimeoutSeconds != maxDiscoverTimeoutSeconds {
		t.Errorf("forwarded timeout_seconds = %v, want clamped to %v", gotBody.TimeoutSeconds, maxDiscoverTimeoutSeconds)
	}
}

func TestDeployTool_SurfacesAPIError(t *testing.T) {
	cli := newFakeAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"kind": "device-busy", "message": "a job is already running for this device"},
		})
	})
	session := connectedClient(t, cli, true)

	res, err := session.CallTool(t.Context(), &sdkmcp.CallToolParams{
		Name: "deploy",
		Arguments: map[string]any{
			"device_id": "deck-1",
			"game_id":   "mygame",
			"directory": "/abs/path",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(deploy) transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected deploy to report a tool error for a busy device, got: %+v", res.Content)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "device-busy") {
		t.Errorf("error text %q does not mention device-busy", text)
	}
}

// Package server implements the versioned local service API from issue
// #13: a loopback-only, bearer-token-authenticated HTTP+SSE surface over
// lazydeck's existing devkit operations (internal/client), so engine
// integrations (the Godot plugin in #14, later a Unity package) consume one
// stable contract instead of importing Go packages, invoking the Python
// bridge directly, or reimplementing the SteamOS Devkit protocol.
//
// Scope of this slice: health, capabilities, device discovery/pairing/
// status/games, asynchronous deploy and log-sync jobs with SSE progress and
// cancellation. Launch/stop are deliberately not implemented yet — see
// handleLaunch/handleStop — because python/cli.py has no launch/stop
// primitive to call; capabilities reports them as unsupported rather than
// guessing at a shape before a real backend exists.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
	"github.com/kevintcoughlin/lazydeck/internal/jobs"
)

// devkitClient is the subset of *client.Client's methods the API surface
// needs. Depending on this interface instead of the concrete type lets
// server_test.go exercise routing, auth, and error mapping with an in-memory
// fake instead of shelling out to a real `uv run` + Python bridge + SSH.
type devkitClient interface {
	Discover(ctx context.Context, timeout time.Duration) ([]client.DiscoveredDevice, error)
	Register(ctx context.Context, machine string) error
	Status(ctx context.Context, machine, login string) (*client.Status, error)
	ListGames(ctx context.Context, machine, login string) ([]any, error)
	Deploy(ctx context.Context, machine, login, gameID, directory string, deleteExtraneous bool) error
	SyncLogs(ctx context.Context, machine, login, gameID, directory string) error
}

// APIVersion is the contract version advertised by /v1/health and
// /v1/capabilities, and embedded in the connection file so a client can
// refuse to talk to an incompatible future/past version instead of getting
// confusing errors deep in a request.
const APIVersion = "v1"

// maxConcurrentJobs bounds total simultaneous mutating operations across all
// devices; each device is additionally limited to one at a time regardless
// of this bound (see internal/jobs). Fixed for this slice rather than
// user-configurable — revisit if fleets large enough to need tuning show up.
const maxConcurrentJobs = 4

// Server holds the dependencies HTTP handlers need: the existing Python
// bridge client, the configured device list, and the job manager.
type Server struct {
	client devkitClient
	cfg    *config.Config
	jobs   *jobs.Manager
	token  string
}

// New builds a Server. token authenticates every /v1/ request.
func New(cli devkitClient, cfg *config.Config, token string) *Server {
	return &Server{
		client: cli,
		cfg:    cfg,
		jobs:   jobs.NewManager(maxConcurrentJobs),
		token:  token,
	}
}

// Handler returns the full HTTP handler: routing plus the bearer-auth
// middleware, which wraps every route (including /v1/health) so a client
// can rely on "any non-401 response means it authenticated" uniformly.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /v1/devices", s.handleListDevices)
	mux.HandleFunc("POST /v1/devices/discover", s.handleDiscover)
	mux.HandleFunc("POST /v1/devices/{id}/pair", s.handlePair)
	mux.HandleFunc("GET /v1/devices/{id}/status", s.handleStatus)
	mux.HandleFunc("GET /v1/devices/{id}/games", s.handleGames)
	mux.HandleFunc("POST /v1/devices/{id}/deployments", s.handleDeploy)
	mux.HandleFunc("POST /v1/devices/{id}/logs/sync", s.handleLogsSync)
	mux.HandleFunc("POST /v1/devices/{id}/games/{gameID}/launch", s.handleLaunch)
	mux.HandleFunc("POST /v1/devices/{id}/games/{gameID}/stop", s.handleStop)
	mux.HandleFunc("GET /v1/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("GET /v1/jobs/{id}/events", s.handleJobEvents)
	mux.HandleFunc("DELETE /v1/jobs/{id}", s.handleCancelJob)

	return s.withAuth(mux)
}

// withAuth requires "Authorization: Bearer <token>" on every request. The
// token is per-launch (see generateToken) and never accepted from a query
// parameter, so it cannot leak into shell history, logs, or a browser's URL
// bar the way a query-string token would.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, prefix) || auth[len(prefix):] != s.token {
			writeError(w, "unauthorized", "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// findDevice resolves a path {id} to its configured Device by Name. Device
// names are already unique (config.AddDevice enforces it), so Name doubles
// as a stable API identifier without inventing a separate ID scheme.
func (s *Server) findDevice(id string) (config.Device, bool) {
	for _, d := range s.cfg.Devices {
		if d.Name == id {
			return d, true
		}
	}
	return config.Device{}, false
}

// deviceOr404 resolves {id} or writes a not-found response and returns ok=false.
func (s *Server) deviceOr404(w http.ResponseWriter, r *http.Request) (config.Device, bool) {
	id := r.PathValue("id")
	d, ok := s.findDevice(id)
	if !ok {
		writeError(w, "not-found", fmt.Sprintf("no configured device %q", id))
		return config.Device{}, false
	}
	return d, true
}

// Run starts `lazydeck serve`: binds an ephemeral loopback port, writes the
// connection file engine plugins read to discover it (see connection.go),
// and serves until ctx is cancelled, at which point it shuts down
// gracefully and removes the connection file it owns.
func Run(ctx context.Context, cli devkitClient, cfg *config.Config) error {
	if err := checkNoOtherInstance(); err != nil {
		return err
	}

	token, err := generateToken()
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("binding loopback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	info := ConnectionInfo{
		PID:        os.Getpid(),
		Port:       port,
		BaseURL:    fmt.Sprintf("http://127.0.0.1:%d", port),
		Token:      token,
		APIVersion: APIVersion,
	}
	connPath, err := writeConnectionInfo(info)
	if err != nil {
		_ = listener.Close()
		return err
	}
	defer removeOwnConnectionFile(connPath, info.PID)

	// The token itself is deliberately not printed: it lives only in the
	// 0600 connection file, so it never lands in shell scrollback, a
	// terminal multiplexer's history, or a captured log of this process's
	// stdout.
	fmt.Printf("lazydeck serve: listening on %s (connection file: %s)\n", info.BaseURL, connPath)

	srv := &http.Server{
		Handler:           New(cli, cfg, token).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("lazydeck serve: graceful shutdown failed: %v", err)
			return err
		}
		return nil
	}
}

// removeOwnConnectionFile deletes the connection file only if it still
// names this process's PID, so a second `lazydeck serve` that failed
// checkNoOtherInstance's race window (vanishingly unlikely, but cheap to
// guard) can never have the first instance's clean shutdown delete the
// second instance's live connection file out from under it.
func removeOwnConnectionFile(path string, pid int) {
	info, err := readConnectionInfo(path)
	if err != nil || info.PID != pid {
		return
	}
	_ = os.Remove(path)
}

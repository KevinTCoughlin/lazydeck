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
	"github.com/kevintcoughlin/lazydeck/internal/notify"
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
	Deploy(ctx context.Context, machine, login, gameID, directory string, deleteExtraneous bool, argv []string) error
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

// shutdownBudget bounds how long Run waits, in total, for the HTTP server's
// graceful shutdown and then for in-flight jobs to actually cancel their
// SSH/rsync subprocess (see internal/client/process_unix.go's own 10s
// WaitDelay backstop) before Run returns anyway.
const shutdownBudget = 30 * time.Second

// Server holds the dependencies HTTP handlers need: the existing Python
// bridge client, the configured device list, and the job manager.
type Server struct {
	client devkitClient
	cfg    *config.Config
	jobs   *jobs.Manager
	token  string
}

// New builds a Server. token authenticates every /v1/ request. It errors if
// cfg's devices don't have unique names: config.Load accepts hand-edited
// TOML with duplicate [[device]] names, but the API treats Device.Name as
// the device's stable ID (see findDevice), so a duplicate would make
// /v1/devices report two IDs that both silently resolve to the first match.
func New(cli devkitClient, cfg *config.Config, token string) (*Server, error) {
	seen := make(map[string]bool, len(cfg.Devices))
	for _, d := range cfg.Devices {
		if seen[d.Name] {
			return nil, fmt.Errorf("devices.toml has more than one device named %q; device names must be unique to be addressable through the API", d.Name)
		}
		seen[d.Name] = true
	}
	s := &Server{
		client: cli,
		cfg:    cfg,
		jobs:   jobs.NewManager(maxConcurrentJobs),
		token:  token,
	}
	if len(cfg.Webhooks) > 0 {
		fanout := make(notify.Fanout, 0, len(cfg.Webhooks))
		for _, url := range cfg.Webhooks {
			fanout = append(fanout, notify.NewWebhook(url))
		}
		s.jobs.SetOnComplete(func(snap jobs.Snapshot) {
			notifyJobComplete(fanout, snap)
		})
	}
	return s, nil
}

// notifyBudget bounds how long a single webhook fanout may take; it runs in
// its own goroutine off the job's execution path, so a slow or unreachable
// webhook can never delay a job being reported terminal to its caller.
const notifyBudget = 10 * time.Second

// notifyJobComplete fans out a finished job's Snapshot to every configured
// webhook. Only Succeeded/Failed are notified — Cancelled jobs are almost
// always a deliberate local action (the "d" cancel keybinding, or process
// shutdown), not something a team channel needs to hear about.
func notifyJobComplete(fanout notify.Fanout, snap jobs.Snapshot) {
	if snap.Status != jobs.Succeeded && snap.Status != jobs.Failed {
		return
	}
	ev := notify.Event{
		DeviceID:  snap.DeviceID,
		Operation: snap.Operation,
		Succeeded: snap.Status == jobs.Succeeded,
		Message:   snap.LastMessage,
	}
	// FinishedAt is always set once a job has reached a terminal status
	// (see jobs.Job.markFinished), so this is the job's actual completion
	// time rather than whenever this callback happened to run.
	if snap.FinishedAt != nil {
		ev.Time = *snap.FinishedAt
	} else {
		ev.Time = time.Now()
	}
	if snap.Error != nil {
		ev.Message = snap.Error.Message
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), notifyBudget)
		defer cancel()
		if err := fanout.Send(ctx, ev); err != nil {
			// The webhook URL itself is deliberately not logged: it's a
			// bearer credential for posting into the destination channel.
			log.Printf("lazydeck serve: webhook notification failed for %s job on device %q: %v", snap.Operation, snap.DeviceID, err)
		}
	}()
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

	return s.withAuth(jsonNotFoundHandler(mux))
}

// jsonNotFoundHandler wraps next so an unmatched route or disallowed
// method gets the API's structured JSON error envelope instead of
// net/http's plain-text default body: every documented /v1 response is
// JSON, and a client that hit a typo'd path deserves the same machine-
// readable {"error": {...}} shape as every other error.
func jsonNotFoundHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&jsonErrorInterceptor{ResponseWriter: w}, r)
	})
}

// jsonErrorInterceptor swallows a plain-text 404/405 body from the wrapped
// handler and substitutes writeErrorStatus's JSON envelope instead; any
// other status passes through untouched.
type jsonErrorInterceptor struct {
	http.ResponseWriter
	status  int
	handled bool
}

func (w *jsonErrorInterceptor) WriteHeader(status int) {
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		w.status = status
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *jsonErrorInterceptor) Write(b []byte) (int, error) {
	if w.status == http.StatusNotFound || w.status == http.StatusMethodNotAllowed {
		if !w.handled {
			w.handled = true
			kind := "not-found"
			if w.status == http.StatusMethodNotAllowed {
				kind = "invalid-input"
			}
			writeErrorStatus(w.ResponseWriter, w.status, kind, http.StatusText(w.status))
		}
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// Flush lets handleJobEvents's `w.(http.Flusher)` assertion keep working
// through this wrapper: embedding http.ResponseWriter alone hides the
// underlying writer's Flusher behind jsonErrorInterceptor's own type, which
// would otherwise silently break SSE streaming for every route.
func (w *jsonErrorInterceptor) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
// gracefully — including cancelling and awaiting any in-flight jobs — and
// removes the connection file it owns.
func Run(ctx context.Context, cli devkitClient, cfg *config.Config) error {
	connPath, err := connectionFilePath()
	if err != nil {
		return err
	}
	connFile, err := acquireConnectionFile(connPath)
	if err != nil {
		return err
	}
	defer func() {
		// Remove while still holding the lock, then close (which releases
		// it): a concurrent `serve` can only ever open+lock this path after
		// we release it, so this can never delete a different, legitimate
		// instance's connection file out from under it.
		_ = os.Remove(connPath)
		_ = connFile.Close()
	}()

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
	if err := writeConnectionInfo(connFile, info); err != nil {
		_ = listener.Close()
		return err
	}

	// The token itself is deliberately not printed: it lives only in the
	// 0600 connection file, so it never lands in shell scrollback, a
	// terminal multiplexer's history, or a captured log of this process's
	// stdout.
	fmt.Printf("lazydeck serve: listening on %s (connection file: %s)\n", info.BaseURL, connPath)

	apiServer, err := New(cli, cfg, token)
	if err != nil {
		_ = listener.Close()
		return err
	}
	srv := &http.Server{
		Handler:           apiServer.Handler(),
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("lazydeck serve: graceful HTTP shutdown failed: %v", err)
		}
		// Cancel and wait for in-flight jobs even if HTTP shutdown above
		// didn't finish cleanly: otherwise a deploy's rsync/ssh could keep
		// running detached after this process has already returned/exited.
		if err := apiServer.jobs.Shutdown(shutdownCtx); err != nil {
			log.Printf("lazydeck serve: in-flight job(s) did not finish before shutdown timed out: %v", err)
			return err
		}
		return nil
	}
}

// Package server implements the versioned local service API from issue
// #13: a loopback, bearer-token-authenticated HTTP+SSE surface over
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
//
// `serve --listen` (issue #48) adds an opt-in second listener for other
// machines on a trusted network. It accepts API keys only and refuses to
// start unless one exists; see checkListen and Options.Listen.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"sync"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/apikey"
	"github.com/kevintcoughlin/lazydeck/internal/audit"
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

	// keys, when set, authenticates long-lived API keys (issue #50) in
	// addition to the session token. audit, when set, receives an entry
	// for every mutating request, scope denial, failed API key, and job
	// outcome (issue #49). Both are wired by Run; tests set them directly.
	keys        *apikey.Store
	audit       *audit.Log
	sessionUser string

	keyNamesMu sync.Mutex
	keyNames   map[string]string // API key id -> name, for job.finished entries
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
	var fanout notify.Fanout
	for _, url := range cfg.Webhooks {
		fanout = append(fanout, notify.NewWebhook(url))
	}
	s.jobs.SetOnComplete(func(snap jobs.Snapshot) {
		s.auditJobFinished(snap)
		if len(fanout) > 0 {
			notifyJobComplete(fanout, snap)
		}
	})
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
func (s *Server) Handler() http.Handler { return s.handler(true) }

// NetworkHandler is Handler for the `serve --listen` listener: the same
// routes, but API keys only (see withAuth).
func (s *Server) NetworkHandler() http.Handler { return s.handler(false) }

func (s *Server) handler(allowSession bool) http.Handler {
	mux := http.NewServeMux()

	// Each route names the API key scope it needs (the session token has
	// them all) and an audit action; mutating routes are audited. Device
	// routes also honor a key's device restriction.
	mux.HandleFunc("GET /v1/health", s.route("", "health", false, s.handleHealth))
	mux.HandleFunc("GET /v1/capabilities", s.route(apikey.ScopeRead, "capabilities", false, s.handleCapabilities))
	mux.HandleFunc("GET /v1/devices", s.route(apikey.ScopeRead, "devices.list", false, s.handleListDevices))
	mux.HandleFunc("POST /v1/devices/discover", s.route(apikey.ScopeDiscover, "devices.discover", true, s.handleDiscover))
	mux.HandleFunc("POST /v1/devices/{id}/pair", s.deviceRoute(apikey.ScopePair, "devices.pair", true, s.handlePair))
	mux.HandleFunc("GET /v1/devices/{id}/status", s.deviceRoute(apikey.ScopeRead, "devices.status", false, s.handleStatus))
	mux.HandleFunc("GET /v1/devices/{id}/games", s.deviceRoute(apikey.ScopeRead, "games.list", false, s.handleGames))
	mux.HandleFunc("POST /v1/devices/{id}/deployments", s.deviceRoute(apikey.ScopeDeploy, "deploy", true, s.handleDeploy))
	mux.HandleFunc("POST /v1/devices/{id}/logs/sync", s.deviceRoute(apikey.ScopeLogsSync, "logs.sync", true, s.handleLogsSync))
	mux.HandleFunc("POST /v1/devices/{id}/games/{gameID}/launch", s.deviceRoute(apikey.ScopeDeploy, "games.launch", true, s.handleLaunch))
	mux.HandleFunc("POST /v1/devices/{id}/games/{gameID}/stop", s.deviceRoute(apikey.ScopeDeploy, "games.stop", true, s.handleStop))
	mux.HandleFunc("GET /v1/jobs/{id}", s.route(apikey.ScopeRead, "jobs.get", false, s.handleGetJob))
	mux.HandleFunc("GET /v1/jobs/{id}/events", s.route(apikey.ScopeRead, "jobs.events", false, s.handleJobEvents))
	mux.HandleFunc("DELETE /v1/jobs/{id}", s.route(apikey.ScopeJobsCancel, "jobs.cancel", true, s.handleCancelJob))

	return s.withAuth(jsonNotFoundHandler(mux), allowSession)
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

// Options configures Run beyond the device config.
type Options struct {
	// Port is the loopback TCP port to bind; 0 picks an ephemeral port.
	// A fixed port lets a CI job running as another OS user, which can't
	// read the 0600 connection file, still find the server.
	Port int
	// APIKeysPath is the API key store (see internal/apikey); "" disables
	// API keys, leaving only the session token.
	APIKeysPath string
	// AuditLogPath is the append-only audit log (see internal/audit).
	AuditLogPath string
	// Listen, when set, adds a second listener at this HOST:PORT that
	// accepts API keys only (issue #48), e.g. "0.0.0.0:47600" so CI
	// runners and teammates on a trusted network can reach this serve.
	// The loopback listener (Port) and its connection file are unchanged.
	// Run refuses to start it unless the key store has an active key.
	Listen string
}

// checkListen validates a --listen address and the auth it requires,
// before anything is bound. It reports whether addr is a loopback address
// (e.g. for a TLS reverse proxy on the same host), which needs no warning.
func checkListen(addr, keysPath string, now time.Time) (loopback bool, err error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false, fmt.Errorf("--listen %q: want HOST:PORT, e.g. 0.0.0.0:47600 or 192.168.10.5:47600", addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return false, fmt.Errorf("--listen %q: port must be between 1 and 65535", addr)
	}
	if host != "" {
		ip := net.ParseIP(host)
		if ip == nil {
			return false, fmt.Errorf("--listen %q: host must be an IP address (or empty for every interface), not a hostname", addr)
		}
		loopback = ip.IsLoopback()
	}
	if keysPath == "" {
		return false, errors.New("refusing to start --listen: API keys are disabled, and the network listener accepts nothing else")
	}
	ok, err := apikey.HasActive(keysPath, now)
	if err != nil {
		return false, fmt.Errorf("refusing to start --listen: %w", err)
	}
	if !ok {
		return false, fmt.Errorf("refusing to start --listen: %s has no active API key, and the network listener accepts only API keys. Create one first, e.g. `lazydeck api-key create --name ci --scopes read,deploy`", keysPath)
	}
	return loopback, nil
}

// listenWarning is printed to stderr whenever --listen binds beyond
// loopback. It is deliberately loud: this is the one way to expose devkit
// deploys to other machines.
func listenWarning(addr string) string {
	return fmt.Sprintf(`lazydeck serve: WARNING: --listen %s accepts connections from other machines.
  - It speaks plain HTTP. API keys, build paths and job output cross the network
    unencrypted. Use it only on a trusted network (a lab VLAN or a VPN). For TLS,
    leave --listen off and put a reverse proxy in front of the loopback port, or
    point the proxy at a loopback --listen address.
  - It accepts API keys only, never the session token. Anyone holding a key can do
    what its scopes allow, on every device it isn't limited from (--devices).
  - Every request is attributed in the audit log; review keys with
    lazydeck api-key list and revoke unused ones.
`, addr)
}

// currentUsername names the OS user behind the session token in audit
// entries; it's informational, so a lookup failure just leaves it blank.
func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// Run starts `lazydeck serve`: binds an ephemeral loopback port, writes the
// connection file engine plugins read to discover it (see connection.go),
// and serves until ctx is cancelled, at which point it shuts down
// gracefully — including cancelling and awaiting any in-flight jobs — and
// removes the connection file it owns.
func Run(ctx context.Context, cli devkitClient, cfg *config.Config, opts Options) error {
	if opts.Port < 0 || opts.Port > 65535 {
		return fmt.Errorf("--port must be between 0 and 65535, got %d", opts.Port)
	}
	// Check --listen's auth requirement before opening or binding
	// anything, so a refusal leaves no connection file or socket behind.
	listenLoopback := false
	if opts.Listen != "" {
		var err error
		if listenLoopback, err = checkListen(opts.Listen, opts.APIKeysPath, time.Now()); err != nil {
			return err
		}
	}
	// Open the audit log before binding anything: serving mutations with
	// no audit trail is worse than not starting (issue #49).
	auditLog, err := audit.Open(opts.AuditLogPath)
	if err != nil {
		return err
	}
	defer func() { _ = auditLog.Close() }()

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

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
	if err != nil {
		return fmt.Errorf("binding loopback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	var netListener net.Listener
	if opts.Listen != "" {
		netListener, err = net.Listen("tcp", opts.Listen)
		if err != nil {
			_ = listener.Close()
			return fmt.Errorf("binding --listen %s: %w", opts.Listen, err)
		}
	}
	closeListeners := func() {
		_ = listener.Close()
		if netListener != nil {
			_ = netListener.Close()
		}
	}

	info := ConnectionInfo{
		PID:        os.Getpid(),
		Port:       port,
		BaseURL:    fmt.Sprintf("http://127.0.0.1:%d", port),
		Token:      token,
		APIVersion: APIVersion,
	}
	if err := writeConnectionInfo(connFile, info); err != nil {
		closeListeners()
		return err
	}

	// The token itself is deliberately not printed: it lives only in the
	// 0600 connection file, so it never lands in shell scrollback, a
	// terminal multiplexer's history, or a captured log of this process's
	// stdout.
	fmt.Printf("lazydeck serve: listening on %s (connection file: %s, audit log: %s)\n", info.BaseURL, connPath, opts.AuditLogPath)
	if netListener != nil {
		fmt.Printf("lazydeck serve: also listening on %s for API keys only\n", opts.Listen)
		if !listenLoopback {
			fmt.Fprint(os.Stderr, listenWarning(opts.Listen))
		}
	}

	apiServer, err := New(cli, cfg, token)
	if err != nil {
		closeListeners()
		return err
	}
	apiServer.audit = auditLog
	apiServer.sessionUser = currentUsername()
	if opts.APIKeysPath != "" {
		apiServer.keys = apikey.NewStore(opts.APIKeysPath)
	}
	loopbackSrv := &http.Server{
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	servers := []*http.Server{loopbackSrv}
	serveErr := make(chan error, 2)
	go func() { serveErr <- loopbackSrv.Serve(listener) }()
	if netListener != nil {
		netSrv := &http.Server{
			Handler:           apiServer.NetworkHandler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		servers = append(servers, netSrv)
		go func() { serveErr <- netSrv.Serve(netListener) }()
	}

	select {
	case err := <-serveErr:
		for _, srv := range servers {
			_ = srv.Close()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
		defer cancel()
		for _, srv := range servers {
			if err := srv.Shutdown(shutdownCtx); err != nil {
				log.Printf("lazydeck serve: graceful HTTP shutdown failed: %v", err)
			}
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

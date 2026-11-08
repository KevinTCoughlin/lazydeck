package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/apikey"
	"github.com/kevintcoughlin/lazydeck/internal/audit"
	"github.com/kevintcoughlin/lazydeck/internal/jobs"
)

// principal is the authenticated caller of one request: either the
// per-launch session token (full access, the local user who started serve)
// or a scoped API key (issue #50).
type principal struct {
	kind string // audit.ActorSession or audit.ActorAPIKey
	key  apikey.Key
	name string // OS user for the session; key name for an API key
}

func (p principal) allows(scope apikey.Scope) bool {
	if p.kind == audit.ActorSession {
		return true
	}
	return p.key.HasScope(scope)
}

// allowsDevice reports whether p may act on the device named id. The
// session token reaches every device; an API key may be limited to some.
func (p principal) allowsDevice(id string) bool {
	if p.kind == audit.ActorSession {
		return true
	}
	return p.key.AllowsDevice(id)
}

// jobActor is the identity recorded on jobs this principal submits, used
// to limit an API key's cancel rights to its own jobs.
func (p principal) jobActor() string {
	if p.kind == audit.ActorAPIKey {
		return audit.ActorAPIKey + ":" + p.key.ID
	}
	return audit.ActorSession
}

func (p principal) auditEntry() audit.Entry {
	e := audit.Entry{Actor: p.kind, ActorName: p.name}
	if p.kind == audit.ActorAPIKey {
		e.ActorID = p.key.ID
	}
	return e
}

type principalKey struct{}

func principalFrom(ctx context.Context) principal {
	p, _ := ctx.Value(principalKey{}).(principal)
	return p
}

// withAuth requires "Authorization: Bearer <credential>" on every request,
// where the credential is either the per-launch session token (see
// generateToken) or an API key from the key store. Credentials are never
// accepted from a query parameter, so they cannot leak into shell history,
// logs, or a browser's URL bar. Both comparisons are constant-time.
//
// allowSession is false for the `serve --listen` network listener (issue
// #48): there the session token is refused even if presented, so the one
// credential that can do everything never has to cross the network and
// every network caller is a scoped, attributable API key.
func (s *Server) withAuth(next http.Handler, allowSession bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		presented, hasBearer := strings.CutPrefix(auth, prefix)

		if hasBearer && subtle.ConstantTimeCompare([]byte(presented), []byte(s.token)) == 1 {
			if !allowSession {
				s.recordAudit(audit.Entry{
					Actor:      audit.ActorAnonymous,
					Action:     "auth",
					Result:     audit.ResultUnauthorized,
					Status:     http.StatusUnauthorized,
					Reason:     "session token refused on the network listener",
					RemoteAddr: r.RemoteAddr,
				})
				writeError(w, "unauthorized", "the session token is only accepted on the loopback listener; use an API key")
				return
			}
			p := principal{kind: audit.ActorSession, name: s.sessionUser}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
			return
		}

		if hasBearer && s.keys != nil && apikey.LooksLikeKey(presented) {
			key, err := s.keys.Authenticate(presented, time.Now())
			if err == nil {
				p := principal{kind: audit.ActorAPIKey, key: key, name: key.Name}
				s.rememberKeyName(key.ID, key.Name)
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
				return
			}
			reason := "invalid API key"
			switch {
			case errors.Is(err, apikey.ErrRevoked):
				reason = "API key revoked"
			case errors.Is(err, apikey.ErrExpired):
				reason = "API key expired"
			case !errors.Is(err, apikey.ErrUnknown):
				// Store unreadable: rejected, fail closed. The error
				// names a file path, never a key.
				log.Printf("lazydeck serve: rejecting API key: %v", err)
				reason = "API key store unavailable"
			}
			s.recordAudit(audit.Entry{
				Actor:      audit.ActorAnonymous,
				ActorID:    apikey.ParseID(presented),
				ActorName:  key.Name,
				Action:     "auth",
				Result:     audit.ResultUnauthorized,
				Status:     http.StatusUnauthorized,
				Reason:     reason,
				RemoteAddr: r.RemoteAddr,
			})
			writeError(w, "unauthorized", "missing or invalid bearer token")
			return
		}

		writeError(w, "unauthorized", "missing or invalid bearer token")
	})
}

// route wraps a handler with its scope check and, for mutating actions,
// an audit entry recording the request's outcome. scope "" means any
// authenticated caller (only /v1/health). Reads aren't audited: they
// change nothing and SSE/polling clients would drown the log. Every scope
// denial is audited, reads included.
func (s *Server) route(scope apikey.Scope, action string, mutating bool, h http.HandlerFunc) http.HandlerFunc {
	return s.routeWith(scope, action, mutating, false, h)
}

// deviceRoute is route for a /v1/devices/{id}/... route: on top of the
// scope check, a device-scoped API key (issue #48) may only reach the
// devices it names. The check runs before the device lookup, so such a key
// gets 403 for every other name and can't probe which devices exist.
func (s *Server) deviceRoute(scope apikey.Scope, action string, mutating bool, h http.HandlerFunc) http.HandlerFunc {
	return s.routeWith(scope, action, mutating, true, h)
}

func (s *Server) routeWith(scope apikey.Scope, action string, mutating, perDevice bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r.Context())
		deny := func(reason, message string) {
			e := p.auditEntry()
			e.Action = action
			e.Device = r.PathValue("id")
			e.Result = audit.ResultDenied
			e.Status = http.StatusForbidden
			e.ErrorKind = "forbidden"
			e.Reason = reason
			e.RemoteAddr = r.RemoteAddr
			s.recordAudit(e)
			writeError(w, "forbidden", message)
		}
		if scope != "" && !p.allows(scope) {
			deny(fmt.Sprintf("missing scope %q", scope), fmt.Sprintf("this credential lacks the %q scope", scope))
			return
		}
		if perDevice && !p.allowsDevice(r.PathValue("id")) {
			deny("key is limited to other devices", "this API key is not allowed to use this device")
			return
		}
		if !mutating {
			h(w, r)
			return
		}

		e := p.auditEntry()
		e.Action = action
		e.RemoteAddr = r.RemoteAddr
		if action != "jobs.cancel" {
			e.Device = r.PathValue("id")
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h(sw, r.WithContext(context.WithValue(r.Context(), auditEntryKey{}, &e)))

		e.Status = sw.status
		switch {
		case sw.status == http.StatusAccepted:
			e.Result = audit.ResultAccepted
		case sw.status < 400:
			e.Result = audit.ResultOK
		case sw.status == http.StatusForbidden:
			e.Result = audit.ResultDenied
			e.ErrorKind = sw.errorKind
		default:
			e.Result = audit.ResultError
			e.ErrorKind = sw.errorKind
		}
		s.recordAudit(e)
	}
}

type auditEntryKey struct{}

// annotateAudit lets a handler add request details (game, directory, job)
// to the audit entry route will write for it. A no-op for unaudited routes.
func annotateAudit(r *http.Request, fn func(*audit.Entry)) {
	if e, ok := r.Context().Value(auditEntryKey{}).(*audit.Entry); ok {
		fn(e)
	}
}

func (s *Server) recordAudit(e audit.Entry) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Write(e); err != nil {
		log.Printf("lazydeck serve: AUDIT LOG WRITE FAILED for %s by %s: %v", e.Action, e.Actor, err)
	}
}

// auditJobFinished records an async job's outcome, attributed to whoever
// submitted it, so the log has both "deploy accepted" and the deploy's
// eventual success or failure.
func (s *Server) auditJobFinished(snap jobs.Snapshot) {
	e := audit.Entry{
		Action:    "job.finished",
		Device:    snap.DeviceID,
		JobID:     snap.ID,
		Result:    string(snap.Status),
		Operation: snap.Operation,
	}
	if snap.FinishedAt != nil {
		e.Time = *snap.FinishedAt
	}
	if snap.Error != nil {
		e.ErrorKind = snap.Error.Kind
	}
	kind, id, _ := strings.Cut(snap.Actor, ":")
	e.Actor = kind
	e.ActorID = id
	switch kind {
	case audit.ActorAPIKey:
		e.ActorName = s.keyName(id)
	case audit.ActorSession:
		e.ActorName = s.sessionUser
	}
	s.recordAudit(e)
}

func (s *Server) keyName(id string) string {
	s.keyNamesMu.Lock()
	defer s.keyNamesMu.Unlock()
	return s.keyNames[id]
}

func (s *Server) rememberKeyName(id, name string) {
	s.keyNamesMu.Lock()
	defer s.keyNamesMu.Unlock()
	if s.keyNames == nil {
		s.keyNames = make(map[string]string)
	}
	s.keyNames[id] = name
}

// statusWriter records the status and error kind a handler wrote, for the
// audit entry. It forwards Flush so it never breaks streaming.
type statusWriter struct {
	http.ResponseWriter
	status    int
	errorKind string
	wrote     bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status, w.wrote = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) setErrorKind(kind string) { w.errorKind = kind }

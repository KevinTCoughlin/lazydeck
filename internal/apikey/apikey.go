// Package apikey implements the long-lived, revocable, scoped API keys
// `lazydeck serve` accepts alongside its per-launch session token (issue
// #50), so a CI job, or a build user other than the one running serve, can
// authenticate with a stable credential that grants only the actions it
// needs.
//
// Keys look like "ldk_<id>_<secret>". The id is a public handle (it appears
// in `lazydeck api-key list` and in the audit log); the secret is 32 random
// bytes. The store (config.APIKeysPath, mode 0600) keeps only the SHA-256
// digest of each full key. A plain digest rather than a password KDF is
// deliberate: the secret is 256 bits of CSPRNG output, so there is nothing
// for a slow hash to protect against brute force, and a KDF would add its
// cost to every authenticated request. Digests are compared with
// crypto/subtle so verification time doesn't depend on how much of a
// guessed key matched.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kevintcoughlin/lazydeck/internal/config"
)

// Scope names one class of action a key may perform. Scopes are explicit:
// there is no wildcard, so adding a new mutating route later can never
// silently widen what an existing key is allowed to do.
type Scope string

const (
	// ScopeRead covers every GET route: devices, status, games, jobs and
	// job event streams, capabilities.
	ScopeRead Scope = "read"
	// ScopeDiscover covers POST /v1/devices/discover (an mDNS browse).
	ScopeDiscover Scope = "discover"
	// ScopePair covers POST /v1/devices/{id}/pair.
	ScopePair Scope = "pair"
	// ScopeDeploy covers POST /v1/devices/{id}/deployments, and the
	// launch/stop routes reserved next to it.
	ScopeDeploy Scope = "deploy"
	// ScopeLogsSync covers POST /v1/devices/{id}/logs/sync.
	ScopeLogsSync Scope = "logs:sync"
	// ScopeJobsCancel covers DELETE /v1/jobs/{id}, limited to jobs the
	// same key started.
	ScopeJobsCancel Scope = "jobs:cancel"
)

// AllScopes lists every scope in display order.
var AllScopes = []Scope{ScopeRead, ScopeDiscover, ScopePair, ScopeDeploy, ScopeLogsSync, ScopeJobsCancel}

// ParseScopes parses a comma-separated scope list, rejecting unknown names
// and an empty list. Duplicates are dropped.
func ParseScopes(csv string) ([]Scope, error) {
	var out []Scope
	for _, part := range strings.Split(csv, ",") {
		name := Scope(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if !slices.Contains(AllScopes, name) {
			return nil, fmt.Errorf("unknown scope %q (valid scopes: %s)", name, joinScopes(AllScopes))
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	return out, nil
}

func joinScopes(scopes []Scope) string {
	names := make([]string, len(scopes))
	for i, s := range scopes {
		names[i] = string(s)
	}
	return strings.Join(names, ",")
}

// Key is one stored API key. It never holds the key itself, only its
// SHA-256 digest.
type Key struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scopes    []Scope    `json:"scopes"`
	SHA256    string     `json:"sha256"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// HasScope reports whether k grants scope.
func (k Key) HasScope(scope Scope) bool { return slices.Contains(k.Scopes, scope) }

// ScopeList renders k's scopes comma-separated.
func (k Key) ScopeList() string { return joinScopes(k.Scopes) }

// State is "active", "expired", or "revoked" at now.
func (k Key) State(now time.Time) string {
	switch {
	case k.RevokedAt != nil:
		return "revoked"
	case k.ExpiresAt != nil && !now.Before(*k.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}

type storeFile struct {
	Version int   `json:"version"`
	Keys    []Key `json:"keys"`
}

const (
	storeVersion = 1
	tokenPrefix  = "ldk_"
	idBytes      = 6  // 12 hex chars: a short public handle
	secretBytes  = 32 // 256 bits of CSPRNG output
	maxNameLen   = 64
)

// Load reads the key store at path. A missing file is an empty store.
func Load(path string) ([]Key, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if f.Version != storeVersion {
		return nil, fmt.Errorf("%s has unsupported version %d (expected %d)", path, f.Version, storeVersion)
	}
	return f.Keys, nil
}

func save(path string, keys []Key) error {
	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Keys: keys}, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

func validateName(name string) error {
	if name == "" {
		return errors.New("a key name is required (e.g. --name ci-build)")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("key name must be at most %d bytes", maxNameLen)
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return errors.New("key name must contain only printable characters")
		}
	}
	return nil
}

// Create generates a new key, appends its digest to the store at path, and
// returns the full key. The returned token is the only copy that will ever
// exist: callers must show it to the user once and not persist or log it.
// ttl <= 0 means the key does not expire.
func Create(path, name string, scopes []Scope, ttl time.Duration, now time.Time) (token string, key Key, err error) {
	if err := validateName(name); err != nil {
		return "", Key{}, err
	}
	if len(scopes) == 0 {
		return "", Key{}, errors.New("at least one scope is required")
	}
	keys, err := Load(path)
	if err != nil {
		return "", Key{}, err
	}
	idRaw := make([]byte, idBytes)
	secretRaw := make([]byte, secretBytes)
	if _, err := rand.Read(idRaw); err != nil {
		return "", Key{}, fmt.Errorf("generating key id: %w", err)
	}
	if _, err := rand.Read(secretRaw); err != nil {
		return "", Key{}, fmt.Errorf("generating key secret: %w", err)
	}
	id := hex.EncodeToString(idRaw)
	token = tokenPrefix + id + "_" + hex.EncodeToString(secretRaw)
	key = Key{
		ID:        id,
		Name:      name,
		Scopes:    slices.Clone(scopes),
		SHA256:    digest(token),
		CreatedAt: now.UTC(),
	}
	if ttl > 0 {
		exp := now.Add(ttl).UTC()
		key.ExpiresAt = &exp
	}
	if err := save(path, append(keys, key)); err != nil {
		return "", Key{}, err
	}
	return token, key, nil
}

// Revoke marks the key with id as revoked. The entry stays in the store so
// audit log lines naming that key id remain attributable to a name.
func Revoke(path, id string, now time.Time) error {
	keys, err := Load(path)
	if err != nil {
		return err
	}
	for i := range keys {
		if keys[i].ID != id {
			continue
		}
		if keys[i].RevokedAt != nil {
			return fmt.Errorf("key %s is already revoked", id)
		}
		t := now.UTC()
		keys[i].RevokedAt = &t
		return save(path, keys)
	}
	return fmt.Errorf("no API key with id %q", id)
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LooksLikeKey reports whether token has an API key's shape, so the server
// can tell a malformed session token from an API key attempt without
// touching the store.
func LooksLikeKey(token string) bool { return strings.HasPrefix(token, tokenPrefix) }

// ParseID extracts the public id from a presented key, or "" if token isn't
// key-shaped. The id is not secret, so it's safe to put in the audit log
// even for a failed attempt.
func ParseID(token string) string {
	rest, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok {
		return ""
	}
	id, _, ok := strings.Cut(rest, "_")
	if !ok || len(id) != 2*idBytes {
		return ""
	}
	if _, err := hex.DecodeString(id); err != nil {
		return ""
	}
	return id
}

// Authentication failure reasons. They are recorded in the audit log but
// the HTTP response stays a generic 401 regardless of which one applied,
// so a caller can't probe which key ids exist.
var (
	ErrUnknown = errors.New("unknown or invalid API key")
	ErrRevoked = errors.New("API key has been revoked")
	ErrExpired = errors.New("API key has expired")
)

// Store authenticates presented keys against the key file, re-reading it
// whenever its modification time or size changes so `lazydeck api-key
// revoke` takes effect on a running server without a restart.
type Store struct {
	path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	present bool
	keys    map[string]Key
	loadErr error
}

// NewStore returns a Store reading path lazily on first use.
func NewStore(path string) *Store { return &Store{path: path} }

// refreshLocked reloads the store if the file changed. If the file can't
// be read or parsed, every key is rejected (fail closed) until it's fixed.
func (s *Store) refreshLocked() {
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.present, s.keys, s.loadErr = false, nil, nil
		return
	}
	if err != nil {
		s.keys, s.loadErr = nil, err
		return
	}
	if s.present && s.loadErr == nil && info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return
	}
	keys, err := Load(s.path)
	s.present, s.modTime, s.size = true, info.ModTime(), info.Size()
	if err != nil {
		s.keys, s.loadErr = nil, err
		return
	}
	s.keys, s.loadErr = make(map[string]Key, len(keys)), nil
	for _, k := range keys {
		s.keys[k.ID] = k
	}
}

// dummyDigest is compared against when the presented id is unknown, so
// the unknown-id path does the same digest work as the known-id path.
var dummyDigest = digest(tokenPrefix + "000000000000_0")

// Authenticate verifies token and returns the matching active key. A
// non-nil error is ErrUnknown, ErrRevoked, ErrExpired, or a store load
// error (fail closed).
func (s *Store) Authenticate(token string, now time.Time) (Key, error) {
	s.mu.Lock()
	s.refreshLocked()
	loadErr := s.loadErr
	key, found := s.keys[ParseID(token)]
	s.mu.Unlock()

	if loadErr != nil {
		return Key{}, fmt.Errorf("API key store unavailable: %w", loadErr)
	}
	want := dummyDigest
	if found {
		want = key.SHA256
	}
	presented := digest(token)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(want)) != 1 || !found {
		return Key{}, ErrUnknown
	}
	switch key.State(now) {
	case "revoked":
		return key, ErrRevoked
	case "expired":
		return key, ErrExpired
	}
	return key, nil
}

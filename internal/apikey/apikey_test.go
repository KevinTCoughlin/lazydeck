package apikey

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func storePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "api-keys.json")
}

func TestCreateStoresOnlyDigestWithPrivateMode(t *testing.T) {
	path := storePath(t)
	token, key, err := Create(path, "ci-build", []Scope{ScopeRead, ScopeDeploy}, 0, t0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ParseID(token) != key.ID {
		t.Fatalf("ParseID(%q) = %q, want %q", token, ParseID(token), key.ID)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	secret := token[strings.LastIndex(token, "_")+1:]
	if strings.Contains(string(data), secret) {
		t.Fatal("key store contains the key's secret; it must hold only a digest")
	}
	if !strings.Contains(string(data), key.SHA256) {
		t.Fatal("key store is missing the key's digest")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("key store mode = %o, want 600", mode)
		}
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	path := storePath(t)
	if _, _, err := Create(path, "", []Scope{ScopeRead}, 0, t0); err == nil {
		t.Error("empty name: expected error")
	}
	if _, _, err := Create(path, "bad\nname", []Scope{ScopeRead}, 0, t0); err == nil {
		t.Error("control character in name: expected error")
	}
	if _, _, err := Create(path, "ok", nil, 0, t0); err == nil {
		t.Error("no scopes: expected error")
	}
}

func TestParseScopes(t *testing.T) {
	got, err := ParseScopes(" read, deploy,read ,logs:sync")
	if err != nil {
		t.Fatal(err)
	}
	want := []Scope{ScopeRead, ScopeDeploy, ScopeLogsSync}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"", " , ", "admin", "read,*"} {
		if _, err := ParseScopes(bad); err == nil {
			t.Errorf("ParseScopes(%q): expected error", bad)
		}
	}
}

func TestAuthenticate(t *testing.T) {
	path := storePath(t)
	token, key, err := Create(path, "ci", []Scope{ScopeRead}, time.Hour, t0)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)

	got, err := s.Authenticate(token, t0)
	if err != nil {
		t.Fatalf("valid key: %v", err)
	}
	if got.ID != key.ID || !got.HasScope(ScopeRead) || got.HasScope(ScopeDeploy) {
		t.Fatalf("unexpected key %+v", got)
	}

	tampered := token[:len(token)-1] + "x"
	if strings.HasSuffix(token, "x") {
		tampered = token[:len(token)-1] + "y"
	}
	for name, presented := range map[string]string{
		"wrong secret": tampered,
		"unknown id":   "ldk_ffffffffffff_" + strings.Repeat("0", 64),
		"not a key":    "garbage",
	} {
		if _, err := s.Authenticate(presented, t0); !errors.Is(err, ErrUnknown) {
			t.Errorf("%s: err = %v, want ErrUnknown", name, err)
		}
	}

	if _, err := s.Authenticate(token, t0.Add(2*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Errorf("after expiry: err = %v, want ErrExpired", err)
	}
}

func TestRevokeTakesEffectWithoutNewStore(t *testing.T) {
	path := storePath(t)
	token, key, err := Create(path, "ci", []Scope{ScopeRead}, 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if _, err := s.Authenticate(token, t0); err != nil {
		t.Fatalf("before revoke: %v", err)
	}
	if err := Revoke(path, key.ID, t0); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := s.Authenticate(token, t0); !errors.Is(err, ErrRevoked) {
		t.Fatalf("after revoke: err = %v, want ErrRevoked", err)
	}
	if err := Revoke(path, key.ID, t0); err == nil {
		t.Error("double revoke: expected error")
	}
	if err := Revoke(path, "nope", t0); err == nil {
		t.Error("unknown id: expected error")
	}
	keys, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].State(t0) != "revoked" {
		t.Fatalf("revoked key should stay listed as revoked, got %+v", keys)
	}
}

func TestCorruptStoreFailsClosed(t *testing.T) {
	path := storePath(t)
	token, _, err := Create(path, "ci", []Scope{ScopeRead}, 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(token, t0)
	if err == nil || errors.Is(err, ErrUnknown) {
		t.Fatalf("corrupt store: err = %v, want a store error", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("store error leaks the presented key")
	}
}

func TestMissingStoreRejectsEverything(t *testing.T) {
	s := NewStore(storePath(t))
	if _, err := s.Authenticate("ldk_000000000000_00", t0); !errors.Is(err, ErrUnknown) {
		t.Fatalf("err = %v, want ErrUnknown", err)
	}
}

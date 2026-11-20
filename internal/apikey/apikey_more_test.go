package apikey

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseDevices(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: "", want: nil},
		{in: " , ,", want: nil},
		{in: "deck", want: []string{"deck"}},
		{in: " deck , lab-deck,deck ", want: []string{"deck", "lab-deck"}},
		{in: "my deck", want: []string{"my deck"}},
		{in: "deck,bad\tname", wantErr: true},
		{in: "bad\x00name", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseDevices(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseDevices(%q): expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDevices(%q): %v", c.in, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("ParseDevices(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeviceScoping(t *testing.T) {
	all := Key{}
	if !all.AllowsDevice("anything") || all.DeviceList() != "all" {
		t.Errorf("unrestricted key: AllowsDevice=%v DeviceList=%q", all.AllowsDevice("anything"), all.DeviceList())
	}
	scoped := Key{Devices: []string{"deck", "lab"}}
	for name, want := range map[string]bool{"deck": true, "lab": true, "other": false, "": false, "Deck": false} {
		if got := scoped.AllowsDevice(name); got != want {
			t.Errorf("AllowsDevice(%q) = %v, want %v", name, got, want)
		}
	}
	if got := scoped.DeviceList(); got != "deck,lab" {
		t.Errorf("DeviceList = %q, want deck,lab", got)
	}
}

func TestCreateForDevicesPersistsRestriction(t *testing.T) {
	path := storePath(t)
	devices := []string{"deck"}
	token, key, err := CreateForDevices(path, "ci", []Scope{ScopeRead, ScopeDeploy}, devices, 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	devices[0] = "mutated"
	if key.Devices[0] != "deck" {
		t.Fatal("CreateForDevices must copy the devices slice")
	}
	got, err := NewStore(path).Authenticate(token, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AllowsDevice("deck") || got.AllowsDevice("other") {
		t.Fatalf("stored key lost its device restriction: %+v", got)
	}
	if got.ScopeList() != "read,deploy" {
		t.Errorf("ScopeList = %q, want read,deploy", got.ScopeList())
	}
}

func TestCreateRejectsLongName(t *testing.T) {
	if _, _, err := Create(storePath(t), strings.Repeat("n", maxNameLen+1), []Scope{ScopeRead}, 0, t0); err == nil {
		t.Error("over-long name: expected error")
	}
	if _, _, err := Create(storePath(t), strings.Repeat("n", maxNameLen), []Scope{ScopeRead}, 0, t0); err != nil {
		t.Errorf("name at the limit: %v", err)
	}
}

func TestStateBoundaries(t *testing.T) {
	exp := t0.Add(time.Hour)
	rev := t0
	cases := []struct {
		name string
		key  Key
		now  time.Time
		want string
	}{
		{"no expiry", Key{}, t0.Add(1000 * time.Hour), "active"},
		{"before expiry", Key{ExpiresAt: &exp}, exp.Add(-time.Nanosecond), "active"},
		{"exactly at expiry", Key{ExpiresAt: &exp}, exp, "expired"},
		{"after expiry", Key{ExpiresAt: &exp}, exp.Add(time.Second), "expired"},
		{"revoked wins over expired", Key{ExpiresAt: &exp, RevokedAt: &rev}, exp.Add(time.Hour), "revoked"},
	}
	for _, c := range cases {
		if got := c.key.State(c.now); got != c.want {
			t.Errorf("%s: State = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHasActive(t *testing.T) {
	t.Run("missing store", func(t *testing.T) {
		ok, err := HasActive(storePath(t), t0)
		if err != nil || ok {
			t.Fatalf("HasActive = %v, %v; want false, nil", ok, err)
		}
	})
	t.Run("only revoked and expired", func(t *testing.T) {
		path := storePath(t)
		_, revoked, err := Create(path, "old", []Scope{ScopeRead}, 0, t0)
		if err != nil {
			t.Fatal(err)
		}
		if err := Revoke(path, revoked.ID, t0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Create(path, "short", []Scope{ScopeRead}, time.Minute, t0); err != nil {
			t.Fatal(err)
		}
		ok, err := HasActive(path, t0.Add(time.Hour))
		if err != nil || ok {
			t.Fatalf("HasActive = %v, %v; want false, nil", ok, err)
		}
		ok, err = HasActive(path, t0)
		if err != nil || !ok {
			t.Fatalf("before expiry: HasActive = %v, %v; want true, nil", ok, err)
		}
	})
	t.Run("corrupt store fails closed", func(t *testing.T) {
		path := storePath(t)
		if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
			t.Fatal(err)
		}
		if ok, err := HasActive(path, t0); err == nil || ok {
			t.Fatalf("HasActive = %v, %v; want false and an error", ok, err)
		}
	})
}

func TestLoadRejectsUnsupportedVersion(t *testing.T) {
	path := storePath(t)
	if err := os.WriteFile(path, []byte(`{"version":2,"keys":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("Load: err = %v, want unsupported version", err)
	}
	// A store at a newer version must fail closed for authentication too.
	if _, err := NewStore(path).Authenticate("ldk_000000000000_00", t0); err == nil || errors.Is(err, ErrUnknown) {
		t.Fatalf("Authenticate: err = %v, want a store error", err)
	}
}

func TestKeyShape(t *testing.T) {
	id := "0123456789ab"
	cases := []struct {
		token     string
		looksLike bool
		id        string
	}{
		{"ldk_" + id + "_secret", true, id},
		{"ldk_" + id + "_", true, id},
		{"ldk_" + id, true, ""},             // no secret separator
		{"ldk_0123456789_secret", true, ""}, // id too short
		{"ldk_0123456789abcd_secret", true, ""},
		{"ldk_0123456789xz_secret", true, ""}, // not hex
		{"ldk__secret", true, ""},
		{"LDK_" + id + "_secret", false, ""},
		{"session-token", false, ""},
		{"", false, ""},
	}
	for _, c := range cases {
		if got := LooksLikeKey(c.token); got != c.looksLike {
			t.Errorf("LooksLikeKey(%q) = %v, want %v", c.token, got, c.looksLike)
		}
		if got := ParseID(c.token); got != c.id {
			t.Errorf("ParseID(%q) = %q, want %q", c.token, got, c.id)
		}
	}
}

func TestStoreSeesKeysAddedAfterFirstUse(t *testing.T) {
	path := storePath(t)
	s := NewStore(path)
	if _, err := s.Authenticate("ldk_000000000000_00", t0); !errors.Is(err, ErrUnknown) {
		t.Fatalf("empty store: err = %v", err)
	}
	token, _, err := Create(path, "late", []Scope{ScopeRead}, 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token, t0); err != nil {
		t.Fatalf("key created after the store was first read: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token, t0); !errors.Is(err, ErrUnknown) {
		t.Fatalf("after the store file is deleted: err = %v, want ErrUnknown", err)
	}
}

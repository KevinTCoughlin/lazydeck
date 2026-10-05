package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"0.2.0", "0.2.0", 0, true},
		{"0.2.0", "0.3.0", -1, true},
		{"1.0.0", "0.9.9", 1, true},
		{"0.10.0", "0.9.0", 1, true},
		{"0.3.0-rc.1", "0.3.0", -1, true},
		{"0.3.0-rc.2", "0.3.0-rc.10", -1, true},
		{"0.2.1-nightly.abc1234", "0.2.0", 1, true},
		{"0.2.1-nightly.abc1234", "0.2.1", -1, true},
		{"0.3.0-rc.1", "0.3.0-rc.1.1", -1, true},
		{"0.3.0-1", "0.3.0-alpha", -1, true},
		{"0.2.0+build.5", "0.2.0", 0, true},
		{"dev", "0.2.0", 0, false},
		{"(devel)", "0.2.0", 0, false},
		{"0.2", "0.2.0", 0, false},
		{"0.2.0-", "0.2.0", 0, false},
	}
	for _, tc := range cases {
		got, ok := compareVersions(tc.a, tc.b)
		if got != tc.want || ok != tc.ok {
			t.Errorf("compareVersions(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCheckForUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "lazydeck/") {
			t.Errorf("User-Agent = %q, want lazydeck/ prefix", ua)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.0","html_url":"https://example.test/v0.3.0"}`))
	}))
	t.Cleanup(srv.Close)

	cases := []struct {
		current, want string
	}{
		{"0.2.0", "lazydeck v0.3.0 is available (you have 0.2.0): https://example.test/v0.3.0"},
		{"v0.3.0", "lazydeck v0.3.0 is up to date (latest release: v0.3.0)."},
		{"0.3.1-nightly.abc1234", "lazydeck 0.3.1-nightly.abc1234 is up to date (latest release: v0.3.0)."},
		{"dev", "Latest release is v0.3.0 (https://example.test/v0.3.0); this build (dev) can't be compared to it."},
	}
	for _, tc := range cases {
		got, err := checkForUpdate(context.Background(), srv.Client(), srv.URL, tc.current)
		if err != nil {
			t.Fatalf("checkForUpdate(%q): %v", tc.current, err)
		}
		if got != tc.want {
			t.Errorf("checkForUpdate(%q) = %q; want %q", tc.current, got, tc.want)
		}
	}
}

func TestCheckForUpdateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	if _, err := checkForUpdate(context.Background(), srv.Client(), srv.URL, "0.2.0"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected a 403 error, got %v", err)
	}
}

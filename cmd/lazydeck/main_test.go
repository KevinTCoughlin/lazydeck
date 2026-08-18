package main

import (
	"strings"
	"testing"
)

// TestVersionStringUsesLdflags guards finding #14: the version command must
// surface ldflags-injected build metadata.
func TestVersionStringUsesLdflags(t *testing.T) {
	oldV, oldC, oldD, oldB := version, commit, date, builtBy
	t.Cleanup(func() { version, commit, date, builtBy = oldV, oldC, oldD, oldB })

	version, commit, date, builtBy = "9.9.9", "deadbeef", "2026-01-02", "goreleaser"
	got := versionString()

	for _, want := range []string{"lazydeck ", "9.9.9", "deadbeef", "2026-01-02", "goreleaser"} {
		if !strings.Contains(got, want) {
			t.Fatalf("version string %q missing %q", got, want)
		}
	}
}

// TestVersionStringFallsBackToBuildInfo verifies the default "dev" build still
// produces a lazydeck-prefixed line (build info fills in when available).
func TestVersionStringFallsBackToBuildInfo(t *testing.T) {
	oldV := version
	t.Cleanup(func() { version = oldV })
	version = "dev"
	got := versionString()
	if !strings.HasPrefix(got, "lazydeck ") {
		t.Fatalf("expected 'lazydeck ' prefix, got %q", got)
	}
}

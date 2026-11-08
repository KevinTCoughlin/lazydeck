package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPIKeyCreateListRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-keys.json")
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)

	var out, errOut bytes.Buffer
	if err := runAPIKey([]string{"create", "--name", "ci-build", "--scopes", "read,deploy", "--expires-in", "720h"}, path, &out, &errOut, now); err != nil {
		t.Fatalf("create: %v (stderr: %s)", err, errOut.String())
	}
	token := strings.TrimSpace(out.String())
	if !strings.HasPrefix(token, "ldk_") || strings.Contains(token, "\n") {
		t.Fatalf("stdout should be exactly the key, got %q", out.String())
	}
	if strings.Contains(errOut.String(), token) {
		t.Fatal("stderr repeats the key")
	}
	id := strings.Split(token, "_")[1]

	out.Reset()
	if err := runAPIKey([]string{"list"}, path, &out, &errOut, now); err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{id, "ci-build", "read,deploy", "active", "2026-12-08"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), token[len("ldk_"+id+"_"):]) {
		t.Fatal("list output contains the key secret")
	}

	out.Reset()
	if err := runAPIKey([]string{"revoke", id}, path, &out, &errOut, now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	out.Reset()
	_ = runAPIKey([]string{"list"}, path, &out, &errOut, now)
	if !strings.Contains(out.String(), "revoked") {
		t.Fatalf("revoked key not shown as revoked:\n%s", out.String())
	}
}

func TestAPIKeyCreateRejectsUnknownScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-keys.json")
	var out, errOut bytes.Buffer
	err := runAPIKey([]string{"create", "--name", "x", "--scopes", "admin"}, path, &out, &errOut, time.Now())
	if err == nil || out.Len() != 0 {
		t.Fatalf("err = %v, stdout = %q; want an error and no key", err, out.String())
	}
}

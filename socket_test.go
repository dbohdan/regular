package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSocketPathRespectsEnv(t *testing.T) {
	t.Setenv(socketEnv, "/tmp/regular-test.sock")

	got, err := defaultSocketPath()
	if err != nil {
		t.Fatalf("defaultSocketPath: %v", err)
	}
	if got != "/tmp/regular-test.sock" {
		t.Errorf("got %q, want override from env", got)
	}
}

func TestDefaultSocketPathFallback(t *testing.T) {
	// Force the temp-dir fallback by clearing the runtime hints.
	t.Setenv(socketEnv, "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	got, err := defaultSocketPath()
	if err != nil {
		t.Fatalf("defaultSocketPath: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty path")
	}
	if filepath.Base(got) != appSocketFileName {
		t.Errorf("expected basename %q, got %q", appSocketFileName, filepath.Base(got))
	}
}

func TestCheckSocketSecurity(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing", func(t *testing.T) {
		if err := checkSocketSecurity(filepath.Join(dir, "nope")); err == nil {
			t.Error("expected error for missing path")
		}
	})

	t.Run("not a socket", func(t *testing.T) {
		path := filepath.Join(dir, "regular-file")
		if err := os.WriteFile(path, []byte("x"), filePerms); err != nil {
			t.Fatal(err)
		}
		if err := checkSocketSecurity(path); err == nil {
			t.Error("expected error for non-socket")
		}
	})

	t.Run("wrong perms", func(t *testing.T) {
		path := filepath.Join(dir, "loose.sock")
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := checkSocketSecurity(path); err == nil {
			t.Error("expected error for 0644 socket")
		}
	})

	t.Run("ok", func(t *testing.T) {
		path := filepath.Join(dir, "ok.sock")
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		if err := os.Chmod(path, filePerms); err != nil {
			t.Fatal(err)
		}
		if err := checkSocketSecurity(path); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

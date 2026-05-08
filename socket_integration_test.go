//go:build integration

package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

// Spawn a daemon against an isolated config + state dir, run a few jobs over
// the socket, and check that streaming, exit codes, and the not-due path
// behave as designed.
func TestDaemonRunOverSocket(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "config")
	stateDir := filepath.Join(tmp, "state")
	sock := filepath.Join(tmp, "regular.sock")

	mustWriteJob(t, configDir, "echoer", `
command = ["sh", "-c", "echo from-daemon-stdout; echo from-daemon-stderr >&2"]
log = False
notify = "never"

def should_run(**_):
    return False
`)
	mustWriteJob(t, configDir, "boomer", `
command = ["sh", "-c", "exit 7"]
log = False
notify = "never"

def should_run(**_):
    return False
`)

	binary := buildBinary(t)

	cmd := exec.Command(binary,
		"--config-dir", configDir,
		"--state-dir", stateDir,
		"--output", "-",
		"start",
	)
	cmd.Env = append(os.Environ(), "REGULAR_SOCK="+sock)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_, _ = cmd.Process.Wait()
	})

	if err := waitForSocket(sock, 3*time.Second); err != nil {
		t.Fatalf("daemon socket never appeared: %v", err)
	}

	t.Run("streams stdout and stderr", func(t *testing.T) {
		stdout, stderr, exit, err := callDaemon(sock, Request{Verb: verbRun, Job: "echoer", Force: true})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if !bytes.Contains(stdout, []byte("from-daemon-stdout")) {
			t.Errorf("missing stdout content: %q", stdout)
		}
		if !bytes.Contains(stderr, []byte("from-daemon-stderr")) {
			t.Errorf("missing stderr content: %q", stderr)
		}
		if exit.Code != 0 {
			t.Errorf("exit code = %d, want 0", exit.Code)
		}
	})

	t.Run("propagates non-zero exit", func(t *testing.T) {
		_, _, exit, err := callDaemon(sock, Request{Verb: verbRun, Job: "boomer", Force: true})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if exit.Code == 0 {
			t.Errorf("expected non-zero exit, got 0 with error %q", exit.Error)
		}
	})

	t.Run("not due exits cleanly without running", func(t *testing.T) {
		_, _, exit, err := callDaemon(sock, Request{Verb: verbRun, Job: "echoer"})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if exit.Code != 0 {
			t.Errorf("not-due path returned exit %d, want 0", exit.Code)
		}
	})

	t.Run("rejects unknown job", func(t *testing.T) {
		_, _, exit, err := callDaemon(sock, Request{Verb: verbRun, Job: "no-such-job", Force: true})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if exit.Error == "" {
			t.Error("expected error in exit frame for unknown job")
		}
	})
}

func mustWriteJob(t *testing.T, configDir, name, body string) {
	t.Helper()
	dir := filepath.Join(configDir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, jobConfigFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "regular")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", path)
}

func callDaemon(sock string, req Request) (stdout, stderr []byte, exit Frame, err error) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, Frame{}, err
	}
	defer conn.Close()

	if err := msgpack.NewEncoder(conn).Encode(req); err != nil {
		return nil, nil, Frame{}, err
	}

	dec := msgpack.NewDecoder(conn)
	var so, se bytes.Buffer
	for {
		var f Frame
		if decErr := dec.Decode(&f); decErr != nil {
			if errors.Is(decErr, net.ErrClosed) {
				return so.Bytes(), se.Bytes(), exit, nil
			}
			return so.Bytes(), se.Bytes(), exit, decErr
		}
		switch f.Type {
		case frameStdout:
			so.Write(f.Data)
		case frameStderr:
			se.Write(f.Data)
		case frameLog:
			// Treat log messages as stderr for assertion convenience.
			se.WriteString(f.Msg + "\n")
		case frameExit:
			return so.Bytes(), se.Bytes(), f, nil
		}
	}
}

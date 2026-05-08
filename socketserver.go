package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	"golang.org/x/sys/unix"
)

// listenSocket creates and binds a Unix-domain listener at path with mode
// 0600. It first tries to detect an already-running daemon and bails if it
// finds one; otherwise it removes a stale socket file and binds.
func listenSocket(path string) (net.Listener, error) {
	// Probe for a live daemon. A short Dial succeeds only if something is
	// actually accepting connections.
	if conn, err := net.DialTimeout("unix", path, 50*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil, errors.New("found another daemon responding on socket")
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerms); err != nil {
		return nil, fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Best-effort cleanup of any stale socket file from a prior crash.
	_ = os.Remove(path)

	// Force 0600 on the new socket.
	old := unix.Umask(0o177)
	defer unix.Umask(old)

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", path, err)
	}

	return listener, nil
}

// serveSocket runs the accept loop until the listener is closed. Each
// connection is handled in its own goroutine.
func serveSocket(listener net.Listener, jsc *jobScheduler, runner jobRunner) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("Socket accept error: %v", err)
			continue
		}

		go handleConn(conn, jsc, runner)
	}
}

// handleConn reads one Request and submits it through the runner, streaming
// stdout/stderr/log/exit frames back to the client.
func handleConn(conn net.Conn, jsc *jobScheduler, runner jobRunner) {
	defer conn.Close()

	dec := msgpack.NewDecoder(conn)
	enc := msgpack.NewEncoder(conn)
	sender := newFrameSender(enc)

	sendExit := func(code int, errMsg string) {
		_ = sender.send(Frame{Type: frameExit, Code: code, Error: errMsg})
	}

	var req Request
	if err := dec.Decode(&req); err != nil {
		sendExit(exitError, fmt.Sprintf("failed to read request: %v", err))
		return
	}

	switch req.Verb {
	case verbRun:
		runOverSocket(jsc, runner, sender, req)
	default:
		sendExit(exitBadUsage, fmt.Sprintf("unknown verb: %q", req.Verb))
	}
}

func runOverSocket(jsc *jobScheduler, runner jobRunner, sender *frameSender, req Request) {
	sendExit := func(code int, errMsg string) {
		_ = sender.send(Frame{Type: frameExit, Code: code, Error: errMsg})
	}
	sendLog := func(msg string) {
		_ = sender.send(Frame{Type: frameLog, Msg: msg})
	}

	jsc.mu.RLock()
	job, ok := jsc.byName[req.Job]
	jsc.mu.RUnlock()
	if !ok {
		sendExit(exitError, fmt.Sprintf("unknown job: %q", req.Job))
		return
	}

	// Stamp on per-request writers and a completion signal.
	done := make(chan CompletedJob, 1)
	job.Stdout = newFrameWriter(sender, frameStdout)
	job.Stderr = newFrameWriter(sender, frameStderr)
	job.OnComplete = func(cj CompletedJob) {
		done <- cj
	}

	if req.Force {
		runner.addJob(job)
	} else {
		lastCompleted, err := runner.lastCompleted(job.Name)
		if err != nil {
			sendExit(exitError, fmt.Sprintf("failed to look up last completion: %v", err))
			return
		}
		shouldRun, err := job.shouldRun(time.Now(), lastCompleted)
		if err != nil {
			sendExit(exitError, fmt.Sprintf("should_run failed: %v", err))
			return
		}
		if !shouldRun {
			sendLog("not due to run; pass --force to override")
			sendExit(exitOK, "")
			return
		}
		runner.addJob(job)
	}

	cj := <-done
	exitCode := cj.ExitStatus
	if cj.Error != "" && exitCode == 0 {
		exitCode = exitError
	}
	sendExit(exitCode, cj.Error)
}

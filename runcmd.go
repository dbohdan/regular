package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/vmihailenco/msgpack/v5"
)

func (r *RunCmd) Run(config Config) error {
	socketPath, err := defaultSocketPath()
	if err != nil {
		return fmt.Errorf("failed to resolve socket path: %w", err)
	}

	if _, statErr := os.Stat(socketPath); statErr == nil {
		// A socket file exists. Verify it's safe before talking to it; on
		// security failure, error out rather than fall back silently.
		if err := checkSocketSecurity(socketPath); err != nil {
			return fmt.Errorf("refusing to use socket %s: %w", socketPath, err)
		}
		failed, err := r.runOverSocket(socketPath)
		if err == nil {
			if failed {
				return errors.New("one or more jobs failed")
			}
			return nil
		}
		// A connection error (e.g. stale socket) drops us into the
		// standalone path.
		log.Printf("Falling back to standalone run after socket error: %v", err)
	}

	return r.runStandalone(config)
}

// runOverSocket dials the daemon for each requested job, streams output
// frames back to stdout/stderr, and reports whether any job failed.
func (r *RunCmd) runOverSocket(socketPath string) (failed bool, err error) {
	for _, jobName := range r.JobNames {
		jobFailed, jobErr := runOneOverSocket(socketPath, jobName, r.Force)
		if jobErr != nil {
			return failed, jobErr
		}
		if jobFailed {
			failed = true
		}
	}
	return failed, nil
}

func runOneOverSocket(socketPath, jobName string, force bool) (failed bool, err error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return false, fmt.Errorf("failed to connect to %s: %w", socketPath, err)
	}
	defer conn.Close()

	enc := msgpack.NewEncoder(conn)
	if err := enc.Encode(Request{Verb: verbRun, Job: jobName, Force: force}); err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}

	dec := msgpack.NewDecoder(conn)
	for {
		var f Frame
		if err := dec.Decode(&f); err != nil {
			if errors.Is(err, io.EOF) {
				return false, fmt.Errorf("connection closed before exit frame")
			}
			return false, fmt.Errorf("failed to read frame: %w", err)
		}

		switch f.Type {
		case frameStdout:
			os.Stdout.Write(f.Data)
		case frameStderr:
			os.Stderr.Write(f.Data)
		case frameLog:
			logJobPrintf(jobName, "%s", f.Msg)
		case frameExit:
			if f.Error != "" {
				logJobPrintf(jobName, "Error: %s", f.Error)
			}
			return f.Code != 0 || f.Error != "", nil
		default:
			logJobPrintf(jobName, "Unknown frame type %q", f.Type)
		}
	}
}

// runStandalone is the no-daemon path. It locks the state directory so two
// concurrent invocations cannot race on the DB or log files.
func (r *RunCmd) runStandalone(config Config) error {
	lockPath := filepath.Join(config.StateRoot, appLockFileName)
	fileLock := flock.New(lockPath)
	locked, err := fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("error checking lock file: %w", err)
	}
	if !locked {
		return fmt.Errorf("another regular instance is using %s", config.StateRoot)
	}
	defer func() {
		_ = fileLock.Unlock()
	}()

	db, err := openAppDB(config.StateRoot)
	if err != nil {
		return err
	}
	defer db.close()

	runner, err := newJobRunner(db, notifyUserByEmail(db), config.StateRoot)
	if err != nil {
		return err
	}

	jobs := newJobScheduler()
	now := time.Now()

	for _, jobName := range r.JobNames {
		path := filepath.Join(config.ConfigRoot, jobName, jobConfigFileName)

		_, job, err := jobs.update(config.ConfigRoot, path)
		if err != nil {
			logJobPrintf(jobNameFromPath(path), "Error loading job: %v", err)
			return nil
		}

		// Either force-run or check should_run.
		if r.Force {
			runner.addJob(*job)
		} else {
			if err := job.addToQueueIfDue(runner, now); err != nil {
				return fmt.Errorf("failed to schedule job %q: %w", job.Name, err)
			}
		}
	}

	// Drain each queue sequentially.
	for queueName := range runner.queues {
		for len(runner.queues[queueName].jobs) > 0 {
			if err := runner.runQueueHead(queueName); err != nil {
				return err
			}
		}
	}

	return nil
}

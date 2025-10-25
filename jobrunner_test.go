package main

import (
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dbohdan.com/denv"
)

func TestJobRunner(t *testing.T) {
	log.SetOutput(io.Discard)

	tmpDir, err := os.MkdirTemp("", "jobrunner-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db, err := openAppDB(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create app database: %v", err)
	}

	runner, err := newJobRunner(db, nil, tmpDir)
	if err != nil {
		t.Fatalf("Failed to create job runner: %v", err)
	}

	// Test adding a job.
	t.Run("AddJob", func(t *testing.T) {
		job := JobConfig{
			Name:    "test-job",
			Command: []string{"echo", "hello"},
			Env:     denv.Env{},
		}
		runner.addJob(job)

		if len(runner.queues["test-job"].jobs) != 1 {
			t.Errorf("Expected 1 job in queue, got %d", len(runner.queues["test-job"].jobs))
		}
	})

	// Test duplicate jobs.
	t.Run("DuplicateJobs", func(t *testing.T) {
		job := JobConfig{
			Duplicate: true,
			Name:      "duplicate-job",
			Command:   []string{"echo", "test"},
			Env:       denv.Env{},
		}

		runner.addJob(job)
		// Add same job again.
		runner.addJob(job)

		if len(runner.queues["duplicate-job"].jobs) != 2 {
			t.Errorf("Expected 2 jobs in queue, got %d", len(runner.queues["duplicate-job"].jobs))
		}
	})

	// Test running a job.
	t.Run("RunJob", func(t *testing.T) {
		job := JobConfig{
			Name:    "run-test-job",
			Command: []string{"echo", "Hello, world!"},
			Env:     denv.OS(),
			Log:     true,
		}
		runner.addJob(job)

		err := runner.runQueueHead("run-test-job")
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		if len(runner.queues["run-test-job"].jobs) != 0 {
			t.Errorf("Expected 0 jobs remaining in queue, got %d", len(runner.queues["run-test-job"].jobs))
		}

		// Verify that the log files were created.
		logFiles := []string{stdoutFileName, stderrFileName}
		for _, f := range logFiles {
			path := filepath.Join(tmpDir, job.Name, f)

			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Errorf("Expected log file %q to exist", path)
			}
		}
	})

	// Test a failed job.
	t.Run("FailedJob", func(t *testing.T) {
		job := JobConfig{
			Name:    "fail-test-job",
			Command: []string{"./regular"},
			Env:     denv.Env{},
		}
		runner.addJob(job)

		err := runner.runQueueHead("fail-test-job")
		if err == nil {
			t.Errorf("Expected an error running job: %v", err)
		}

		completed, err := runner.lastCompleted("fail-test-job")
		if err != nil {
			t.Errorf("Failed to get completed job: %v", err)
			return
		}
		if completed == nil {
			t.Error("Expected completed job record, got nil")
			return
		}
		if completed.ExitStatus != 2 {
			t.Errorf("Expected exit status 2, got %d", completed.ExitStatus)
		}
	})

	// Test the queue summary.
	t.Run("QueueSummary", func(t *testing.T) {
		summary := runner.summarize()

		if summary == "" {
			t.Error("Expected non-empty queue summary")
		}
	})
}

func TestFuncRunCommand(t *testing.T) {
	tests := []struct {
		name       string
		command    []string
		wantErr    bool
		exitStatus int
	}{
		{
			name:       "successful command",
			command:    []string{"true"},
			wantErr:    false,
			exitStatus: 0,
		},
		{
			name:       "failed command",
			command:    []string{"false"},
			wantErr:    true,
			exitStatus: 1,
		},
		{
			name:       "nonexistent command",
			command:    []string{"this-is-a-nonexistent-command"},
			wantErr:    true,
			exitStatus: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runCommand(tt.name, denv.Env{}, "", tt.command, nil, nil, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("runCommand() error = %v, wantErr %v", err, tt.wantErr)
			}

			var exitErr *exec.ExitError
			exitStatus := 0
			if errors.As(err, &exitErr) {
				exitStatus = exitErr.ExitCode()
			}
			if exitStatus != tt.exitStatus {
				t.Errorf("runCommand() exit status = %d, want %d", exitStatus, tt.exitStatus)
			}
		})
	}
}

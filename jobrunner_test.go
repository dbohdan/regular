package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"dbohdan.com/regular/envfile"
	"mvdan.cc/sh/v3/interp"
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
			Name:   "test-job",
			Script: "echo 'hello'",
			Env:    envfile.Env{},
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
			Script:    "echo 'test'",
			Env:       envfile.Env{},
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
			Name:   "run-test-job",
			Script: "echo 'Hello, world!'",
			Env:    envfile.OS(),
			Log:    true,
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
			Name:   "fail-test-job",
			Script: "exit 1",
			Env:    envfile.Env{},
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
		if completed.ExitStatus != 1 {
			t.Errorf("Expected exit status 1, got %d", completed.ExitStatus)
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

func TestRunScript(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantErr    bool
		exitStatus int
	}{
		{
			name:       "successful script",
			script:     "exit 0",
			wantErr:    false,
			exitStatus: 0,
		},
		{
			name:       "failed script",
			script:     "exit 1",
			wantErr:    true,
			exitStatus: 1,
		},
		{
			name:       "invalid script",
			script:     "invalid command",
			wantErr:    true,
			exitStatus: 127,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runScript(tt.name, envfile.Env{}, "", tt.script, nil, nil, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("runScript() error = %v, wantErr %v", err, tt.wantErr)
			}
			if status, ok := interp.IsExitStatus(err); ok && int(status) != tt.exitStatus {
				t.Errorf("runScript() exit status = %d, want %d", status, tt.exitStatus)
			}
		})
	}
}

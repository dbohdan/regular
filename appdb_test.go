package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJobRunnerDB(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "jobrunnerdb-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Open a test database.
	db, err := openAppDB(tmpDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.close()

	// Test saving and retrieving a completed job.
	jobName := "test-job"
	now := time.Now()
	completed := CompletedJob{
		Error:      "test error",
		ExitStatus: 1,
		Started:    now.Add(-time.Minute),
		Finished:   now,
	}

	// Create test log files.
	logDir := filepath.Join(tmpDir, "logs")
	if err := os.MkdirAll(logDir, dirPerms); err != nil {
		t.Fatalf("Failed to create log directory: %v", err)
	}

	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")

	if err := os.WriteFile(stdoutPath, []byte("test stdout\nline 2\n"), filePerms); err != nil {
		t.Fatalf("Failed to write stdout file: %v", err)
	}
	if err := os.WriteFile(stderrPath, []byte("test stderr\n"), filePerms); err != nil {
		t.Fatalf("Failed to write stderr file: %v", err)
	}

	logs := []logFile{
		{name: "stdout", path: stdoutPath},
		{name: "stderr", path: stderrPath},
	}

	// Test `saveCompletedJob`.
	if err := db.saveCompletedJob(jobName, completed, logs); err != nil {
		t.Errorf("Failed to save completed job: %v", err)
	}

	// Test `getLastCompleted`.
	lastCompleted, err := db.getLastCompleted(jobName)
	if err != nil {
		t.Errorf("Failed to get last completed job: %v", err)
	}

	if lastCompleted == nil {
		t.Fatal("Expected last completed job, got nil")
	}

	if lastCompleted.Error != completed.Error {
		t.Errorf("Expected error %q, got %q", completed.Error, lastCompleted.Error)
	}

	if lastCompleted.ExitStatus != completed.ExitStatus {
		t.Errorf("Expected exit status %d, got %d", completed.ExitStatus, lastCompleted.ExitStatus)
	}

	// Test `getJobLogs`.
	stdoutLogs, err := db.getJobLogs(jobName, "stdout", 10)
	if err != nil {
		t.Errorf("Failed to get stdout logs: %v", err)
	}
	if len(stdoutLogs) != 2 {
		t.Errorf("Expected 2 stdout lines, got %d", len(stdoutLogs))
	}
	if stdoutLogs[0] != "test stdout" {
		t.Errorf("Expected first line %q, got %q", "test stdout", stdoutLogs[0])
	}

	stderrLogs, err := db.getJobLogs(jobName, "stderr", 10)
	if err != nil {
		t.Errorf("Failed to get stderr logs: %v", err)
	}
	if len(stderrLogs) != 1 {
		t.Errorf("Expected 1 stderr line, got %d", len(stderrLogs))
	}
	if stderrLogs[0] != "test stderr" {
		t.Errorf("Expected first line %q, got %q", "test stderr", stderrLogs[0])
	}

	// Test with a nonexistent job.
	nonexistentJob, err := db.getLastCompleted("nonexistent")
	if err != nil {
		t.Errorf("Failed to query nonexistent job: %v", err)
	}

	if nonexistentJob != nil {
		t.Error("Expected nil for nonexistent job")
	}
}

package main

import (
	"path/filepath"
	"testing"
)

func TestNewJobScheduler(t *testing.T) {
	jsc := newJobScheduler()

	if jsc.byName == nil {
		t.Error(`"byName" map should be initialized`)
	}

	if jsc.mu == nil {
		t.Error("mutex should be initialized")
	}
}

func TestJobSchedulerUpdate(t *testing.T) {
	jsc := newJobScheduler()

	configRoot := t.TempDir()
	jobDir := filepath.Join(configRoot, "test-job")
	jobPath := filepath.Join(jobDir, jobFileName)

	// Test updating a job with no job file.
	result, _, err := jsc.update(configRoot, jobPath)
	if err == nil {
		t.Error("expected error when updating non-existent job")
	}

	if result != jobsNoChanges {
		t.Errorf(`expected "jobsNoChanges", got %v`, result)
	}
}

func TestJobSchedulerRemove(t *testing.T) {
	jsc := newJobScheduler()

	// Test removing a non-existent job.
	err := jsc.remove("nonexistent")
	if err == nil {
		t.Error("expected error when removing non-existent job")
	}

	// Add a mock job and test removal.
	jsc.byName["test-job"] = JobConfig{}

	err = jsc.remove("test-job")
	if err != nil {
		t.Errorf("unexpected error removing existing job: %v", err)
	}

	if _, exists := jsc.byName["test-job"]; exists {
		t.Error("job should have been removed")
	}
}

func TestJobNameFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/jobdir/job.star", "jobdir"},
		{"jobdir/job.star", "jobdir"},
		{"job.star", "."},
	}

	for _, tt := range tests {
		result := jobNameFromPath(tt.path)
		if result != tt.expected {
			t.Errorf("jobNameFromPath(%q) = %q, want %q", tt.path, result, tt.expected)
		}
	}
}

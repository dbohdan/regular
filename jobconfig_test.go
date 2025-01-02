package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"dbohdan.com/regular/envfile"
	"go.starlark.net/starlark"
)

func TestJobConfigQueueName(t *testing.T) {
	tests := []struct {
		name     string
		job      JobConfig
		expected string
	}{
		{
			name: "empty queue returns job name",
			job: JobConfig{
				Name:  "test-job",
				Queue: "",
			},
			expected: "test-job",
		},
		{
			name: "specified queue returns queue name",
			job: JobConfig{
				Name:  "test-job",
				Queue: "custom-queue",
			},
			expected: "custom-queue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.job.QueueName()

			if result != tt.expected {
				t.Errorf("QueueName() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestLoadJob(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "jobconfig-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	jobContent := `
duplicates = False
enabled = True
env["TEST_VAR"] = "test_value"
jitter = 5
log = True
notify = "always"
queue = "test-queue"

script = """
sleep 1
"""

def should_run(**_):
    return True
`

	jobPath := filepath.Join(tmpDir, "job.star")
	if err := os.WriteFile(jobPath, []byte(jobContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Test loading the job.
	env := envfile.Env{"INITIAL_VAR": "initial_value"}
	job, err := loadJob(env, jobPath)
	if err != nil {
		t.Fatalf("loadJob() error = %v", err)
	}

	// Verify job properties.
	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{"Enabled", job.Enabled, true},
		{"Script", job.Script, "\nsleep 1\n"},
		{"Duplicates", job.Duplicates, false},
		{"Log", job.Log, true},
		{"Queue", job.Queue, "test-queue"},
		{"Jitter", job.Jitter, 5 * time.Second},
		{"Name", job.Name, filepath.Base(filepath.Dir(jobPath))},
		{"Notify", job.Notify, notifyMode("always")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.expected)
			}
		})
	}

	// Test environment variables.
	if v, ok := job.Env["TEST_VAR"]; !ok || v != "test_value" {
		t.Errorf(`Env["TEST_VAR"] = %q, want "test_value"`, v)
	}

	if v, ok := job.Env["INITIAL_VAR"]; !ok || v != "initial_value" {
		t.Errorf(`Env["INITIAL_VAR"] = %q, want "initial_value"`, v)
	}

	// Test the `should_run` function.
	thread := &starlark.Thread{Name: "test"}
	result, err := starlark.Call(thread, job.ShouldRun, nil, nil)
	if err != nil {
		t.Errorf(`"should_run" call failed: %v`, err)
	}
	if result != starlark.True {
		t.Errorf(`"should_run" returned %v, want "True"`, result)
	}
}

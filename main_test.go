package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	commandRegular = "./regular"
)

func createTempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "regular-test-*")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	err = os.Mkdir(filepath.Join(dir, "config"), dirPerms)
	if err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	err = os.Mkdir(filepath.Join(dir, "status"), dirPerms)
	if err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

	return dir
}

func command(args ...string) (string, string, error) {
	cmd := exec.Command(commandRegular, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

func commandWithDirs(tempRoot string, args ...string) (string, string, error) {
	configRoot := filepath.Join(tempRoot, "config")
	stateRoot := filepath.Join(tempRoot, "state")
	allArgs := append(
		[]string{"--config-root", configRoot, "--state-root", stateRoot},
		args...,
	)

	return command(allArgs...)
}

func TestNoArgs(t *testing.T) {
	_, stderr, _ := command()

	if matched, _ := regexp.MatchString("expected one of", stderr); !matched {
		t.Error("Expected 'expected one of' in stderr")
	}
}

func TestVersion(t *testing.T) {
	stdout, _, _ := command("--version")

	if matched, _ := regexp.MatchString(`\d+\.\d+\.\d+`, stdout); !matched {
		t.Error("Expected version format in stdout")
	}
}

func TestListCommandHelp(t *testing.T) {
	stdout, _, err := command("list", "--help")

	if err != nil {
		t.Errorf("Expected no error for 'list --help', got %v", err)
	}

	if !strings.Contains(stdout, "List available jobs") {
		t.Error("Expected 'List available jobs' in stdout")
	}
}

func TestListCommand(t *testing.T) {
	tempDir := createTempDir(t)
	_, _, err := commandWithDirs(tempDir, "list")

	if err != nil {
		t.Errorf("Expected no error for 'list', got %v", err)
	}
}

func TestLogCommandHelp(t *testing.T) {
	stdout, _, err := command("log", "--help")

	if err != nil {
		t.Errorf("Expected no error for 'log --help', got %v", err)
	}

	if !strings.Contains(stdout, "Show application log") {
		t.Error("Expected 'Show application log' in stdout")
	}
}

func TestLogCommand(t *testing.T) {
	tempDir := createTempDir(t)
	_, _, err := commandWithDirs(tempDir, "log")

	if err != nil {
		t.Errorf("Expected no error for 'log', got %v", err)
	}
}

func TestRunCommandHelp(t *testing.T) {
	stdout, _, err := command("run", "--help")

	if err != nil {
		t.Errorf("Expected no error for 'run --help', got %v", err)
	}

	if !strings.Contains(stdout, "Run jobs once") {
		t.Error("Expected 'Run jobs once' in stdout")
	}
}

func TestRunCommand(t *testing.T) {
	tempDir := createTempDir(t)
	_, _, err := commandWithDirs(tempDir, "run")

	if err != nil {
		t.Errorf("Expected no error for 'run', got %v", err)
	}
}

func TestStartCommandHelp(t *testing.T) {
	stdout, _, err := command("start", "--help")

	if err != nil {
		t.Errorf("Expected no error for 'start --help', got %v", err)
	}

	if !strings.Contains(stdout, "Start scheduler") {
		t.Error("Expected 'Start scheduler' in stdout")
	}
}

func TestStatusCommandHelp(t *testing.T) {
	stdout, _, err := command("status", "--help")

	if err != nil {
		t.Errorf("Expected no error for 'status --help', got %v", err)
	}

	if !strings.Contains(stdout, "Show job status") {
		t.Error("Expected 'Show job status' in stdout")
	}
}

func TestStatusCommand(t *testing.T) {
	tempDir := createTempDir(t)
	_, _, err := commandWithDirs(tempDir, "status")

	if err != nil {
		t.Errorf("Expected no error for 'status', got %v", err)
	}
}

func TestStatusLogLines(t *testing.T) {
	tempDir := createTempDir(t)
	_, _, err := commandWithDirs(tempDir, "status", "-l", "5")

	if exitErr, ok := err.(*exec.ExitError); ok {
		t.Errorf("Expected status command to run, got exit code %d", exitErr.ExitCode())
	}
}

func TestStatusInvalidConfigDir(t *testing.T) {
	stdout, _, err := command("status", "--config-root", "/nonexistent/path")

	if _, ok := err.(*exec.ExitError); !ok {
		t.Error("Expected error for invalid config directory")
	}

	if !strings.Contains(stdout, "error looking for jobs in config dir") {
		t.Error("Expected 'error looking for jobs in config dir' in stdout")
	}
}

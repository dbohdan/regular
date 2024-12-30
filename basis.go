package main

import (
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

const (
	completedJobFileName = "completed.json"
	dirName              = "regular"
	envFileName          = "env"
	jobFileName          = "job.star"
	stderrFileName       = "stderr.log"
	stdoutFileName       = "stdout.log"

	dirPerms  = 0700
	filePerms = 0600

	enabledVar   = "enabled"
	envVar       = "env"
	jobDirEnvVar = "REGULAR_JOB_DIR"
	shouldRunVar = "should_run"

	debounceInterval = 100 * time.Millisecond
	scheduleInterval = time.Second
)

var (
	defaultConfigRoot = filepath.Join(xdg.ConfigHome, dirName)
	defaultStateRoot  = filepath.Join(xdg.StateHome, dirName)
)

type Config struct {
	ConfigRoot string
	StateRoot  string
}

func jobDir(path string) string {
	return filepath.Dir(path)
}

func jobNameFromPath(path string) string {
	return filepath.Base(filepath.Dir(path))
}

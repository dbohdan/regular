package main

import (
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

const (
	dirName             = "regular"
	envFileName         = "env"
	jobFileName         = "job.star"
	jobRunnerDBFileName = "state.sqlite3"
	stderrFileName      = "stderr.log"
	stdoutFileName      = "stdout.log"

	jobDirEnvVar = "REGULAR_JOB_DIR"

	enabledVar    = "enabled"
	envVar        = "env"
	notifyModeVar = "notify"
	oneDayVar     = "one_day"
	oneHourVar    = "one_hour"
	oneMinuteVar  = "one_minute"
	shouldRunVar  = "should_run"

	redactedValue = "[redacted]"
	secretRegexp  = "(?i)(key|password|secret|token)"

	dirPerms  = 0700
	filePerms = 0600

	timestampFormat = "2006-01-02 15:04:05 -0700"

	debounceInterval = 100 * time.Millisecond
	runInterval      = time.Second
	scheduleInterval = time.Minute

	defaultLogLines = 10
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

package main

import (
	"path/filepath"
	"regexp"
	"time"

	"github.com/adrg/xdg"
)

const (
	version = "0.2.0"

	appDBFileName         = "state.sqlite3"
	appLockFileName       = "app.lock"
	appLogFileName        = "app.log"
	dirName               = "regular"
	globalEnvFileName     = "global.env"
	jobConfigFileName     = "config.star"
	jobEnvFileName        = "job.env"
	jobExecutableFileName = "./job"
	stderrFileName        = "stderr.log"
	stdoutFileName        = "stdout.log"

	jobDirEnvVar = "REGULAR_JOB_DIR"

	enableVar     = "enable"
	envVar        = "env"
	logVar        = "log"
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
	maxMissedTime    = time.Hour
	runInterval      = time.Second
	scheduleInterval = time.Minute

	defaultLogLines  = 10
	maxLogBufferSize = 256 * 1024
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

func boolYesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}

// Format a Duration without the trailing zero units.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Millisecond)

	if d > time.Second {
		d = d.Round(100 * time.Millisecond)
	}

	zeroUnits := regexp.MustCompile("(^|[^0-9])(?:0h)?(?:0m)?(?:0s)?$")
	s := zeroUnits.ReplaceAllString(d.String(), "$1")

	if s == "" {
		return "0"
	}

	return s
}

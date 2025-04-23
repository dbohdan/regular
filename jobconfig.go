package main

import (
	"fmt"
	"time"

	"github.com/mna/starstruct"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"dbohdan.com/regular/envfile"
	"dbohdan.com/regular/starlarkutil"
)

type JobConfig struct {
	Command   []string       `starlark:"command"`
	Duplicate bool           `starlark:"duplicate"`
	Enabled   bool           `starlark:"enabled"`
	Env       envfile.Env    `starlark:"-"`
	Jitter    time.Duration  `starlark:"jitter"`
	Log       bool           `starlark:"log"`
	Name      string         `starlark:"-"`
	Notify    notifyMode     `starlark:"-"`
	Queue     string         `starlark:"queue"`
	ShouldRun starlark.Value `starlark:"should_run"`
}

func (j JobConfig) QueueName() string {
	if j.Queue == "" {
		return j.Name
	}

	return j.Queue
}

func (j JobConfig) shouldRun(t time.Time, lastCompleted *CompletedJob) (bool, error) {
	if !j.Enabled {
		return false, nil
	}

	exitStatus := -1
	finished := -1
	started := -1
	if lastCompleted != nil {
		exitStatus = lastCompleted.ExitStatus
		finished = int(lastCompleted.Finished.Unix())
		started = int(lastCompleted.Started.Unix())
	}

	kvpairs := []starlark.Tuple{
		starlark.Tuple{
			starlark.String("minute"),
			starlark.MakeInt(t.Minute()),
		},
		starlark.Tuple{
			starlark.String("hour"),
			starlark.MakeInt(t.Hour()),
		},
		starlark.Tuple{
			starlark.String("day"),
			starlark.MakeInt(t.Day()),
		},
		starlark.Tuple{
			starlark.String("month"),
			starlark.MakeInt(int(t.Month())),
		},
		starlark.Tuple{
			starlark.String("dow"),
			starlark.MakeInt(int(t.Weekday())),
		},
		starlark.Tuple{
			starlark.String("timestamp"),
			starlark.MakeInt(int(t.Unix())),
		},
		starlark.Tuple{
			starlark.String("exit_status"),
			starlark.MakeInt(exitStatus),
		},
		starlark.Tuple{
			starlark.String("finished"),
			starlark.MakeInt(finished),
		},
		starlark.Tuple{
			starlark.String("started"),
			starlark.MakeInt(started),
		},
	}

	thread := &starlark.Thread{Name: "schedule"}
	result, err := starlark.Call(thread, j.ShouldRun, nil, kvpairs)
	if err != nil {
		return false, fmt.Errorf(`failed to call "should_run": %v`, err)
	}

	switch result {

	case starlark.False:
		return false, nil

	case starlark.True:
		return true, nil

	default:
		return false, fmt.Errorf(`"should_run" returned bad value: %v`, result)
	}
}

func (j JobConfig) addToQueueIfDue(runner jobRunner, t time.Time) error {
	lastCompleted, err := runner.lastCompleted(j.Name)
	if err != nil {
		return err
	}

	shouldRun, err := j.shouldRun(t, lastCompleted)
	if err != nil {
		return err
	}

	if shouldRun {
		runner.addJob(j)
	}

	return nil
}

func loadJob(env envfile.Env, path string) (JobConfig, error) {
	thread := &starlark.Thread{Name: "job"}

	job := JobConfig{
		Name: jobNameFromPath(path),
	}

	envDict := starlark.NewDict(len(env))
	for k, v := range env {
		if err := envDict.SetKey(starlark.String(k), starlark.String(v)); err != nil {
			return job, fmt.Errorf("failed to set env dict key: %w", err)
		}
	}

	predeclared := starlark.StringDict{
		envVar:       envDict,
		oneDayVar:    starlark.MakeInt(24 * 60 * 60),
		oneHourVar:   starlark.MakeInt(60 * 60),
		oneMinuteVar: starlark.MakeInt(60),
	}
	starlarkutil.AddPredeclared(predeclared)

	globals, err := starlark.ExecFileOptions(
		&syntax.FileOptions{},
		thread,
		path,
		nil,
		predeclared,
	)
	if err != nil {
		return job, err
	}

	stringDict := starlark.StringDict(globals)

	if err := starstruct.FromStarlark(stringDict, &job); err != nil {
		return job, fmt.Errorf(`failed to convert job to struct: %w`, err)
	}

	if len(job.Command) == 0 {
		job.Command = []string{jobExecutableFileName}
	}

	enabledValue, exists := globals[enabledVar]
	job.Enabled = !exists || enabledValue == starlark.True

	logValue, exists := globals[logVar]
	job.Log = !exists || logValue == starlark.True

	finalEnvDict := envDict
	_, exists = globals[envVar]
	if exists {
		var ok bool
		finalEnvDict, ok = globals[envVar].(*starlark.Dict)
		if !ok {
			return job, fmt.Errorf("%q isn't a dictionary", envVar)
		}
	}

	job.Env = make(envfile.Env)
	for _, item := range finalEnvDict.Items() {
		key, ok := item.Index(0).(starlark.String)
		if !ok {
			return job, fmt.Errorf("%q key %q must be Starlark string", envVar, item.Index(0))
		}

		value, ok := item.Index(1).(starlark.String)
		if !ok {
			return job, fmt.Errorf("%q value %q isn't Starlark string", envVar, item.Index(1))
		}

		job.Env[key.GoString()] = value.GoString()
	}

	job.Jitter *= time.Second

	notifyModeString := ""
	notifyModeValue, exists := globals[notifyModeVar]
	if exists {
		value, ok := notifyModeValue.(starlark.String)
		if !ok {
			return job, fmt.Errorf("%q must be Starlark string", notifyModeVar)
		}

		notifyModeString = value.GoString()
	}
	job.Notify, _ = parseNotifyMode(notifyModeString)

	return job, nil
}

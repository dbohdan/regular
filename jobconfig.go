package main

import (
	"fmt"
	"time"

	"dbohdan.com/regular/starlarkutil"
	"github.com/mna/starstruct"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

type JobConfig struct {
	Duplicates bool           `starlark:"duplicates"`
	Enabled    bool           `starlark:"enabled"`
	Env        Env            `starlark:"-"`
	Jitter     time.Duration  `starlark:"jitter"`
	Name       string         `starlark:"-"`
	Queue      string         `starlark:"queue"`
	Script     string         `starlark:"script"`
	ShouldRun  starlark.Value `starlark:"should_run"`
}

func (j JobConfig) schedule(runner jobRunner) error {
	if !j.Enabled {
		return nil
	}

	lastCompleted := runner.lastCompleted(j.Name)

	finished := -1
	if lastCompleted != nil {
		finished = int(lastCompleted.Finished.Unix())
	}

	now := time.Now()
	kvpairs := []starlark.Tuple{
		starlark.Tuple{
			starlark.String("minute"),
			starlark.MakeInt(now.Minute()),
		},
		starlark.Tuple{
			starlark.String("hour"),
			starlark.MakeInt(now.Hour()),
		},
		starlark.Tuple{
			starlark.String("day"),
			starlark.MakeInt(now.Day()),
		},
		starlark.Tuple{
			starlark.String("month"),
			starlark.MakeInt(int(now.Month())),
		},
		starlark.Tuple{
			starlark.String("dow"),
			starlark.MakeInt(int(now.Weekday())),
		},
		starlark.Tuple{
			starlark.String("timestamp"),
			starlark.MakeInt(int(now.Unix())),
		},
		starlark.Tuple{
			starlark.String("finished"),
			starlark.MakeInt(finished),
		},
	}

	thread := &starlark.Thread{Name: "schedule"}
	result, err := starlark.Call(thread, j.ShouldRun, nil, kvpairs)
	if err != nil {
		return fmt.Errorf(`failed to call "should_run": %v`, err)
	}

	switch result {

	case starlark.False:

	case starlark.True:
		runner.addJob(j)

	default:
		return fmt.Errorf(`"should_run" returned bad value: %v`, result)
	}

	return nil
}

func loadJob(env Env, path string) (JobConfig, error) {
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
		enabledVar: starlark.True,
		envVar:     envDict,
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

	finalEnvDict := envDict
	_, exists := globals[envVar]
	if exists {
		var ok bool
		finalEnvDict, ok = globals[envVar].(*starlark.Dict)
		if !ok {
			return job, fmt.Errorf("%q isn't a dictionary", envVar)
		}
	}

	job.Env = make(Env)
	for _, item := range finalEnvDict.Items() {
		key, ok := item.Index(0).(starlark.String)
		if !ok {
			return job, fmt.Errorf("%q key %v must be Starlark string", envVar, item.Index(0))
		}

		value, ok := item.Index(1).(starlark.String)
		if !ok {
			return job, fmt.Errorf("%q value %v must be Starlark string", envVar, item.Index(1))
		}

		job.Env[key.GoString()] = value.GoString()
	}

	job.Enabled = predeclared[enabledVar] == starlark.True
	job.Jitter *= time.Second

	return job, nil
}

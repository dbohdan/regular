package main

import (
	"encoding/json"
	"strings"
	"time"

	"go.starlark.net/starlark"
)

type Env map[string]string

func (e Env) Pairs() []string {
	pairs := []string{}

	for k, v := range e {
		pairs = append(pairs, k+"="+v)
	}

	return pairs
}

func envFromPairs(pairs []string) Env {
	env := make(Env)

	for _, pair := range pairs {
		split := strings.SplitN(pair, "=", 2)
		key := split[0]
		value := ""
		if len(split) == 2 {
			value = split[1]
		}

		env[key] = value
	}

	return env
}

type JobResult interface {
	GetName() string
}

type BaseJobResult struct {
	Name string
}

func (b BaseJobResult) GetName() string {
	return b.Name
}

type Log struct {
	Filename string
	Lines    []string
	Modified float64
}

type JobResultCompleted struct {
	BaseJobResult
	ExitStatus int
	Stdout     Log
	Stderr     Log
}

type JobResultError struct {
	BaseJobResult
	Message string
	Log     string
}

type JobResultLocked struct {
	BaseJobResult
}

type JobResultSkipped struct {
	BaseJobResult
}

type Notifier interface {
	Notify(result JobResult)
}

type Config struct {
	ConfigRoot string
	StateRoot  string
}

type Notify string

const (
	NotifyNever   Notify = "never"
	NotifyAlways  Notify = "always"
	NotifyOnError Notify = "on error"
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

type CompletedJob struct {
	Error      string
	ExitStatus int       `json:"exit_status"`
	Started    time.Time `json:"started"`
	Finished   time.Time `json:"finished"`
	StdoutFile string    `json:"stdout"`
	StderrFile string    `json:"stderr"`
}

func (cj CompletedJob) MarshalJSON() ([]byte, error) {
	type Alias CompletedJob

	return json.Marshal(&struct {
		Started  string `json:"started"`
		Finished string `json:"finished"`
		*Alias
	}{
		Started:  cj.Started.Format(time.RFC3339),
		Finished: cj.Finished.Format(time.RFC3339),
		Alias:    (*Alias)(&cj),
	})
}

func UnmarshalCompletedJob(data []byte) (CompletedJob, error) {
	type Alias CompletedJob
	var cj CompletedJob

	stringTimes := &struct {
		Started  string `json:"started"`
		Finished string `json:"finished"`
		*Alias
	}{
		Alias: (*Alias)(&cj),
	}

	var err error
	if err = json.Unmarshal(data, &stringTimes); err != nil {
		return cj, err
	}

	cj.Started, err = time.Parse(time.RFC3339, stringTimes.Started)
	if err != nil {
		return cj, err
	}

	cj.Finished, err = time.Parse(time.RFC3339, stringTimes.Finished)
	if err != nil {
		return cj, err
	}

	return cj, nil
}

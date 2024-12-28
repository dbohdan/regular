package main

import (
	"time"

	"go.starlark.net/starlark"
)

type Env map[string]string

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

type Job struct {
	Command   []string       `starlark:"command"`
	Enabled   bool           `starlark:"enabled"`
	Env       Env            `starlark:"env"`
	Jitter    time.Duration  `starlark:"jitter"`
	Name      string         `starlark:"name"`
	Queue     string         `starlark:"queue"`
	ShouldRun starlark.Value `starlark:"should_run"`
}

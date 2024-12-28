package main

import (
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

type Job struct {
	Dir       string         `starlark:"-"`
	Enabled   bool           `starlark:"enabled"`
	Env       Env            `starlark:"-"`
	Jitter    time.Duration  `starlark:"jitter"`
	Name      string         `starlark:"name"`
	Queue     string         `starlark:"queue"`
	Script    string         `starlark:"script"`
	ShouldRun starlark.Value `starlark:"should_run"`
}

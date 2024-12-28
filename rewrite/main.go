package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/bep/debounce"
	"github.com/cornfeedhobo/pflag"
	"github.com/fsnotify/fsnotify"
	"github.com/joho/godotenv"
	"github.com/mna/starstruct"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	shsyntax "mvdan.cc/sh/v3/syntax"

	"dbohdan.com/regular/starlarkutil"
)

const (
	dirName     = "regular"
	envFileName = "env"
	jobFileName = "job.star"

	enabledVar   = "enabled"
	envVar       = "env"
	shouldRunVar = "should_run"

	debounceInterval = 100 * time.Millisecond
)

var (
	defaultConfigRoot = filepath.Join(xdg.ConfigHome, dirName)
	defaultStateRoot  = filepath.Join(xdg.StateHome, dirName)
)

func jobDir(path string) string {
	return filepath.Dir(path)
}

func jobNameFromPath(path string) string {
	return filepath.Base(filepath.Dir(path))
}

func loadEnv(startEnv Env, envPath ...string) (Env, error) {
	loadedEnv, err := godotenv.Read(envPath...)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to read env files: %w", err)
	}

	env := make(Env)
	maps.Copy(env, startEnv)
	maps.Copy(env, loadedEnv)

	return env, nil
}

func loadJob(env Env, path string) (Job, error) {
	thread := &starlark.Thread{Name: "job"}

	job := Job{
		Dir: jobDir(path),
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

func runScript(jobName string, env Env, script string) error {
	parser := shsyntax.NewParser()

	prog, err := parser.Parse(strings.NewReader(script), jobName)
	if err != nil {
		return fmt.Errorf("failed to parse shell script: %v", err)
	}

	runner, err := interp.New(
		interp.Env(expand.ListEnviron(env.Pairs()...)),
	)
	if err != nil {
		return fmt.Errorf("failed to create shell interpreter: %v", err)
	}

	if err := runner.Run(context.Background(), prog); err != nil {
		return err
	}

	return nil
}

type Jobs struct {
	byName map[string]Job
	mu     *sync.RWMutex
}

func newJobs() Jobs {
	return Jobs{
		byName: make(map[string]Job),
		mu:     &sync.RWMutex{},
	}
}

func (j Job) run() error {
	if !j.Enabled {
		return nil
	}

	now := time.Now()
	args := starlark.Tuple{
		starlark.MakeInt(now.Minute()),
		starlark.MakeInt(now.Hour()),
		starlark.MakeInt(now.Day()),
		starlark.MakeInt(int(now.Month())),
		starlark.MakeInt(int(now.Weekday())),
	}

	fn, ok := j.ShouldRun.(*starlark.Function)
	if !ok {
		return fmt.Errorf("%q is not a function", shouldRunVar)
	}
	args = args[:min(len(args), fn.NumParams())]

	thread := &starlark.Thread{Name: "scheduler"}
	result, err := starlark.Call(thread, j.ShouldRun, args, nil)
	if err != nil {
		return fmt.Errorf(`failed to call "should_run": %v`, err)
	}

	switch result {

	case starlark.False:

	case starlark.True:

		if err := runScript(j.Name, j.Env, j.Script); err != nil {
			return fmt.Errorf("script error: %v", err)
		}

	default:
		return fmt.Errorf(`"should_run" returned bad value: %v`, result)
	}

	return nil
}

func (j Jobs) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		j.mu.RLock()

		for name, job := range j.byName {
			logJobPrintf(name, "Running job")

			if err := job.run(); err != nil {
				logJobPrintf(name, "Error running job: %v", err)
			}

			logJobPrintf(name, "Finished job")
		}

		j.mu.RUnlock()
	}
}

type updateJobsResult int

const (
	jobsNoChanges updateJobsResult = iota
	jobsAddedNew
	jobsUpdated
)

func (j Jobs) update(path string) (updateJobsResult, error) {
	jobDir := jobDir(path)
	jobName := jobNameFromPath(path)

	osEnv := envFromPairs(os.Environ())
	env, err := loadEnv(osEnv, filepath.Join(jobDir, envFileName))
	if err != nil {
		return jobsNoChanges, fmt.Errorf("failed to load job env: %v", err)
	}

	job, err := loadJob(env, path)
	if err != nil {
		return jobsNoChanges, fmt.Errorf("failed to load job: %v", err)
	}

	j.mu.Lock()
	_, exists := j.byName[jobName]
	j.byName[jobName] = job
	j.mu.Unlock()

	if exists {
		return jobsUpdated, nil
	}

	return jobsAddedNew, nil
}

func (j Jobs) remove(name string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	_, exists := j.byName[name]
	if !exists {
		return fmt.Errorf("failed to find job to remove: %v", name)
	}

	delete(j.byName, name)
	return nil
}

func (j Jobs) watchChanges(watcher *fsnotify.Watcher) {
	debounced := debounce.New(debounceInterval)

	for {
		select {

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			eventPath := event.Name

			handleUpdate := func(updatePath string) {
				jobName := jobNameFromPath(updatePath)

				res, err := j.update(updatePath)
				if err != nil {
					removeErr := j.remove(jobName)

					if removeErr == nil {
						logJobPrintf(jobName, "Job removed after update error: %v", err)
					} else {
						logJobPrintf(jobName, "Failed to remove job after update error: %v", err)
					}
				}

				switch res {

				case jobsNoChanges:

				case jobsUpdated:
					logJobPrintf(jobName, "Updated job")

				case jobsAddedNew:
					logJobPrintf(jobName, "Added job")
				}
			}

			if filepath.Base(eventPath) == jobFileName {
				jobName := jobNameFromPath(eventPath)

				if event.Has(fsnotify.Write) {
					debounced(func() {
						handleUpdate(eventPath)
					})
				} else if event.Has(fsnotify.Remove) {
					err := j.remove(jobName)
					if err == nil {
						logJobPrintf(jobName, "Removed job")
					} else {
						logJobPrintf(jobName, "Failed to remove job: %v", err)
					}
				}
			}

			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(eventPath); err == nil && info.IsDir() {
					_ = watcher.Add(eventPath)

					jobFilePath := filepath.Join(eventPath, jobFileName)
					if _, err := os.Stat(jobFilePath); err == nil {
						handleUpdate(jobFilePath)
					}
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}

			log.Printf("Watcher error: %v", err)
		}
	}
}

type logWriter struct{}

func (writer logWriter) Write(bytes []byte) (int, error) {
	timestamp := time.Now().Format("2006-01-02 15:04:05 -0700")
	return fmt.Printf("[%s] %s", timestamp, string(bytes))
}

func logJobPrintf(job, format string, v ...any) {
	values := append([]any{job}, v...)
	log.Printf("[%s] "+format, values...)
}

func cli() Config {
	configRoot := pflag.StringP("config", "c", defaultConfigRoot, "path to config directory")
	stateRoot := pflag.StringP("state", "s", defaultStateRoot, "path to state directory")

	pflag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			"Usage: %s [options]\n\nOptions:\n",
			filepath.Base(os.Args[0]),
		)

		pflag.PrintDefaults()
	}

	pflag.Parse()

	return Config{
		ConfigRoot: *configRoot,
		StateRoot:  *stateRoot,
	}
}

func main() {
	jobs := newJobs()

	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	config := cli()

	err = filepath.Walk(config.ConfigRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return watcher.Add(path)
		}

		if filepath.Base(path) == jobFileName {
			_, err := jobs.update(path)
			if err != nil {
				logJobPrintf(jobNameFromPath(path), "Error at startup: %v", err)
			}
		}

		return nil
	})
	if err != nil {
		log.Fatalf("Error walking config dir: %v", err)
	}

	go jobs.run()
	go jobs.watchChanges(watcher)

	// Block forever.
	<-make(chan struct{})
}

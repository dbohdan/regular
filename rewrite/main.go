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

	defaultQueue = "main"

	debounceInterval = 100 * time.Millisecond
	scheduleInterval = time.Second
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

func runScript(jobName string, env Env, script string) error {
	parser := shsyntax.NewParser()

	prog, err := parser.Parse(strings.NewReader(script), jobName)
	if err != nil {
		return fmt.Errorf("failed to parse shell script: %v", err)
	}

	interpreter, err := interp.New(
		interp.Env(expand.ListEnviron(env.Pairs()...)),
	)
	if err != nil {
		return fmt.Errorf("failed to create shell interpreter: %v", err)
	}

	if err := interpreter.Run(context.Background(), prog); err != nil {
		return err
	}

	return nil
}

type JobStore struct {
	byName map[string]JobConfig

	mu *sync.RWMutex
}

func newJobStore() JobStore {
	return JobStore{
		byName: make(map[string]JobConfig),

		mu: &sync.RWMutex{},
	}
}

type jobQueue struct {
	jobs []JobConfig

	mu *sync.RWMutex
}

func newJobQueue() jobQueue {
	return jobQueue{
		jobs: []JobConfig{},

		mu: &sync.RWMutex{},
	}
}

type jobRunner struct {
	queues    map[string]jobQueue
	stateRoot string

	mu *sync.RWMutex
}

func newJobRunner(stateRoot string) jobRunner {
	return jobRunner{
		queues:    make(map[string]jobQueue),
		stateRoot: stateRoot,

		mu: &sync.RWMutex{},
	}
}

func (r jobRunner) addJob(job JobConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	queueName := job.Queue
	if queueName == "" {
		queueName = defaultQueue
	}

	queue, ok := r.queues[queueName]
	if !ok {
		queue = newJobQueue()
		r.queues[queueName] = queue
	}

	queue.mu.Lock()
	defer queue.mu.Unlock()

	if !job.Duplicates {
		for _, otherJob := range queue.jobs {
			if otherJob.Name == job.Name {
				return
			}
		}
	}

	queue.jobs = append(queue.jobs, job)
	r.queues[queueName] = queue

	logJobPrintf(job.Name, "Put job in runner queue: %v", queueName)
}

func (r jobRunner) runQueueHead(queueName string) {
	if ok := r.mu.TryLock(); !ok {
		return
	}
	defer r.mu.Unlock()

	queue, ok := r.queues[queueName]
	if !ok {
		log.Printf("Requested to run head of nonexistent queue: %v", queueName)
		return
	}

	if len(queue.jobs) == 0 {
		return
	}

	job := queue.jobs[0]
	queue.jobs = queue.jobs[1:]
	r.queues[queueName] = queue

	logJobPrintf(job.Name, "Running job")
	completed := CompletedJob{}
	completed.Started = time.Now()

	err := runScript(job.Name, job.Env, job.Script)
	if err != nil {
		if status, ok := interp.IsExitStatus(err); ok {
			completed.ExitStatus = int(status)
		}
		logJobPrintf(job.Name, "Script error: %v", err)
	}

	logJobPrintf(job.Name, "Finished")
	completed.Finished = time.Now()
}

func (r jobRunner) run() {
	ticker := time.NewTicker(scheduleInterval)
	defer ticker.Stop()

	for range ticker.C {
		names := []string{}

		r.mu.RLock()
		for queueName, _ := range r.queues {
			names = append(names, queueName)
		}
		r.mu.RUnlock()

		for _, queueName := range names {
			go r.runQueueHead(queueName)
		}
	}
}

// This function doesn't lock the runner or the queues.
// It is left to the caller.
func (r jobRunner) summarize() string {
	var sb strings.Builder

	for queueName, queue := range r.queues {
		sb.WriteString(queueName + ": ")

		for i, job := range queue.jobs {
			sb.WriteString(job.Name)

			if i != len(queue.jobs)-1 {
				sb.WriteString(", ")
			}
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

func (j JobConfig) schedule(runner jobRunner) error {
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

	thread := &starlark.Thread{Name: "schedule"}
	result, err := starlark.Call(thread, j.ShouldRun, args, nil)
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

func (jst JobStore) schedule(runner jobRunner) {
	ticker := time.NewTicker(scheduleInterval)
	defer ticker.Stop()

	for range ticker.C {
		jst.mu.RLock()

		for name, job := range jst.byName {
			err := job.schedule(runner)
			if err != nil {
				logJobPrintf(name, "Error scheduling job: %v", err)
			}
		}

		jst.mu.RUnlock()
	}
}

type updateJobsResult int

const (
	jobsNoChanges updateJobsResult = iota
	jobsAddedNew
	jobsUpdated
)

func (jst JobStore) update(path string) (updateJobsResult, error) {
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

	jst.mu.Lock()
	_, exists := jst.byName[jobName]
	jst.byName[jobName] = job
	jst.mu.Unlock()

	if exists {
		return jobsUpdated, nil
	}

	return jobsAddedNew, nil
}

func (jst JobStore) remove(name string) error {
	jst.mu.Lock()
	defer jst.mu.Unlock()

	_, exists := jst.byName[name]
	if !exists {
		return fmt.Errorf("failed to find job to remove: %v", name)
	}

	delete(jst.byName, name)
	return nil
}

func (jst JobStore) watchChanges(watcher *fsnotify.Watcher) {
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

				res, err := jst.update(updatePath)
				if err != nil {
					removeErr := jst.remove(jobName)

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
					err := jst.remove(jobName)
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
	jobs := newJobStore()

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

	runner := newJobRunner(config.StateRoot)

	go jobs.schedule(runner)
	go jobs.watchChanges(watcher)
	go runner.run()

	// Block forever.
	<-make(chan struct{})
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adrg/xdg"
	"github.com/alecthomas/kong"
	"github.com/bep/debounce"
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

type RunCmd struct{}

type StatusCmd struct{}

type CLI struct {
	Run    RunCmd    `cmd:"" help:"Run scheduler"`
	Status StatusCmd `cmd:"" help:"Show job status"`

	ConfigRoot string `help:"Path to config directory" default:"${defaultConfigRoot}" type:"path"`
	StateRoot  string `help:"Path to state directory" default:"${defaultStateRoot}" type:"path"`
}

func (r *RunCmd) Run(config Config) error {
	return runService(config)
}

func (s *StatusCmd) Run() error {
	// TODO: Implement the status command.
	return fmt.Errorf("status command not yet implemented")
}

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

func runScript(jobName string, env Env, script string, stdin io.Reader, stdout, stderr io.Writer) error {
	parser := shsyntax.NewParser()

	prog, err := parser.Parse(strings.NewReader(script), jobName)
	if err != nil {
		return fmt.Errorf("failed to parse shell script: %v", err)
	}

	interpreter, err := interp.New(
		interp.Env(expand.ListEnviron(env.Pairs()...)),
		interp.StdIO(stdin, stdout, stderr),
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
	activeJob bool
	jobs      []JobConfig

	mu *sync.RWMutex
}

func newJobQueue() jobQueue {
	return jobQueue{
		jobs: []JobConfig{},

		mu: &sync.RWMutex{},
	}
}

type jobRunner struct {
	completed map[string][]CompletedJob
	queues    map[string]jobQueue
	stateRoot string

	mu *sync.RWMutex
}

func newJobRunner(stateRoot string) jobRunner {
	return jobRunner{
		completed: make(map[string][]CompletedJob),
		queues:    make(map[string]jobQueue),
		stateRoot: stateRoot,

		mu: &sync.RWMutex{},
	}
}

func (r jobRunner) lastCompleted(jobName string) *CompletedJob {
	r.mu.RLock()
	defer r.mu.RUnlock()

	jobCompleted, ok := r.completed[jobName]
	if !ok || len(jobCompleted) == 0 {
		return nil
	}

	return &jobCompleted[len(jobCompleted)-1]
}

func (r jobRunner) addJob(job JobConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	queueName := job.Queue
	if queueName == "" {
		queueName = job.Name
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

	logJobPrintf(
		job.Name,
		"Put job in runner queue of length %v: %v",
		len(queue.jobs),
		queueName,
	)
}

func (cj CompletedJob) save(jobStateDir string) error {
	if err := os.MkdirAll(jobStateDir, dirPerms); err != nil {
		return fmt.Errorf("failed to create state directory: %v", err)
	}

	filename := filepath.Join(jobStateDir, completedJobFileName)

	jsonData, err := cj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal completed job: %v", err)
	}

	if err := os.WriteFile(filename, jsonData, filePerms); err != nil {
		return fmt.Errorf("failed to write completed job data: %v", err)
	}

	return nil
}

func (r jobRunner) runQueueHead(queueName string) error {
	r.mu.RLock()
	queue, ok := r.queues[queueName]
	r.mu.RUnlock()

	if !ok {
		log.Printf("Requested to run head of nonexistent queue: %v", queueName)
		return nil
	}

	if queue.activeJob || len(queue.jobs) == 0 {
		return nil
	}

	queue.mu.Lock()
	job := queue.jobs[0]
	queue.jobs = queue.jobs[1:]
	queue.mu.Unlock()

	r.mu.Lock()
	queue.activeJob = true
	r.queues[queueName] = queue
	r.mu.Unlock()

	cj := CompletedJob{}
	cj.Started = time.Now()
	logJobPrintf(job.Name, "Started")

	jobStateDir := filepath.Join(r.stateRoot, job.Name)

	stdoutFile, err := os.OpenFile(
		filepath.Join(jobStateDir, stdoutFileName),
		os.O_RDWR|os.O_CREATE,
		filePerms,
	)
	stderrFile, err := os.OpenFile(
		filepath.Join(jobStateDir, stderrFileName),
		os.O_RDWR|os.O_CREATE,
		filePerms,
	)

	runErr := runScript(job.Name, job.Env, job.Script, nil, stdoutFile, stderrFile)
	cj.Error = ""
	if runErr != nil {
		cj.Error = runErr.Error()
	}
	if status, ok := interp.IsExitStatus(runErr); ok {
		cj.ExitStatus = int(status)
	}

	logJobPrintf(job.Name, "Finished")
	cj.Finished = time.Now()

	r.mu.Lock()
	completed, ok := r.completed[job.Name]
	if ok {
		completed = append(completed, cj)
	} else {
		completed = []CompletedJob{cj}
	}
	r.completed[job.Name] = completed

	queue, ok = r.queues[queueName]
	if ok {
		queue.activeJob = false
		r.queues[queueName] = queue
	}

	err = cj.save(jobStateDir)
	r.mu.Unlock()

	if err != nil {
		return fmt.Errorf("failed to save completed job: %w", err)
	}

	if runErr != nil {
		return fmt.Errorf("script failed: %w", runErr)
	}

	return nil
}

// Wraps an error with a job name.
type JobError struct {
	JobName string
	Err     error
}

func (e *JobError) Error() string {
	return fmt.Sprintf("job %q: %v", e.JobName, e.Err)
}

func newJobError(jobName string, err error) *JobError {
	return &JobError{JobName: jobName, Err: err}
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}

	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}

	return string(unicode.ToUpper(r)) + s[size:]
}

func withLog(f func() error) {
	if err := f(); err != nil {
		if je, ok := err.(*JobError); ok {
			logJobPrintf(je.JobName, "%v", capitalizeFirst(je.Err.Error()))
		} else {
			log.Printf("%v", err)
		}
	}
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
			go withLog(func() error {
				return r.runQueueHead(queueName)
			})
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

func (jst JobStore) schedule(runner jobRunner) error {
	ticker := time.NewTicker(scheduleInterval)
	defer ticker.Stop()

	for range ticker.C {
		jst.mu.RLock()

		for name, job := range jst.byName {
			err := job.schedule(runner)
			if err != nil {
				return newJobError(name, fmt.Errorf("scheduling error: %w", err))
			}
		}

		jst.mu.RUnlock()
	}
	return nil
}

type updateJobsResult int

const (
	jobsNoChanges updateJobsResult = iota
	jobsAddedNew
	jobsUpdated
)

func (jst JobStore) update(configRoot, jobPath string) (updateJobsResult, error) {
	jobDir := jobDir(jobPath)
	jobName := jobNameFromPath(jobPath)

	osEnv := envFromPairs(os.Environ())
	globalEnv, err := loadEnv(osEnv, filepath.Join(configRoot, envFileName))
	if err != nil {
		return jobsNoChanges, fmt.Errorf("failed to load global env: %v", err)
	}
	jobEnv, err := loadEnv(globalEnv, filepath.Join(jobDir, envFileName))
	if err != nil {
		return jobsNoChanges, fmt.Errorf("failed to load job env: %v", err)
	}

	jobEnv[jobDirEnvVar] = jobDir

	job, err := loadJob(jobEnv, jobPath)
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

func (jst JobStore) watchChanges(configRoot string, watcher *fsnotify.Watcher) error {
	debounced := debounce.New(debounceInterval)

	for {
		select {

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			eventPath := event.Name

			handleUpdate := func(updatePath string) {
				jobName := jobNameFromPath(updatePath)

				res, err := jst.update(configRoot, updatePath)
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
				return nil
			}

			return fmt.Errorf("watcher error: %w", err)
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

func runService(config Config) error {
	jobs := newJobStore()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	err = filepath.Walk(config.ConfigRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return watcher.Add(path)
		}

		if filepath.Base(path) == jobFileName {
			_, err := jobs.update(config.ConfigRoot, path)
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

	go withLog(func() error {
		return jobs.schedule(runner)
	})
	go withLog(func() error {
		return jobs.watchChanges(config.ConfigRoot, watcher)
	})
	go runner.run()

	// Block forever.
	<-make(chan struct{})
	return nil
}

func main() {
	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("regular"),
		kong.Description("Run regular jobs."),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
		kong.Exit(func(code int) {
			if code == 1 {
				code = 2
			}

			os.Exit(code)
		}),
		kong.Vars{
			"defaultConfigRoot": defaultConfigRoot,
			"defaultStateRoot":  defaultStateRoot,
		},
	)

	config := Config{
		ConfigRoot: cli.ConfigRoot,
		StateRoot:  cli.StateRoot,
	}

	err := ctx.Run(config)
	if err != nil {
		log.Fatal(err)
	}
}

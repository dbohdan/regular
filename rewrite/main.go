package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/bep/debounce"
	"github.com/cornfeedhobo/pflag"
	"github.com/fsnotify/fsnotify"
	"github.com/mna/starstruct"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const (
	dirName     = "regular"
	jobFileName = "job.star"

	enabledVar   = "enabled"
	shouldRunVar = "should_run"
)

var (
	defaultConfigRoot = filepath.Join(xdg.ConfigHome, dirName)
	defaultStateRoot  = filepath.Join(xdg.StateHome, dirName)
)

func jobNameFromPath(path string) string {
	return filepath.Base(filepath.Dir(path))
}

func loadJob(path string) (Job, error) {
	thread := &starlark.Thread{Name: "job"}

	globals, err := starlark.ExecFileOptions(
		&syntax.FileOptions{},
		thread,
		path,
		nil,
		nil,
	)
	if err != nil {
		return Job{}, err
	}

	if _, ok := globals[enabledVar]; !ok {
		globals[enabledVar] = starlark.True
	}

	stringDict := starlark.StringDict(globals)

	var job Job
	if err := starstruct.FromStarlark(stringDict, &job); err != nil {
		return Job{}, fmt.Errorf(`failed to convert job dictionary: %w`, err)
	}
	job.Jitter *= time.Second

	return job, nil
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

func (j Jobs) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		thread := &starlark.Thread{Name: "scheduler"}

		j.mu.RLock()

		for name, job := range j.byName {
			if !job.Enabled {
				continue
			}

			args := starlark.Tuple{
				starlark.MakeInt(now.Minute()),
				starlark.MakeInt(now.Hour()),
				starlark.MakeInt(now.Day()),
				starlark.MakeInt(int(now.Month())),
				starlark.MakeInt(int(now.Weekday())),
			}

			fn, ok := job.ShouldRun.(*starlark.Function)
			if !ok {
				logJobPrintf(name, "%q is not a function", shouldRunVar)
				continue
			}
			args = args[:min(len(args), fn.NumParams())]

			result, err := starlark.Call(thread, job.ShouldRun, args, nil)
			if err != nil {
				logJobPrintf(name, `Error calling "should_run": %v`, err)
				continue
			}

			switch result {

			case starlark.False:

			case starlark.True:
				logJobPrintf(name, "Running job")

			default:
				logJobPrintf(name, `"should_run" returned bad value: %v`, result)
			}
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
	jobName := jobNameFromPath(path)

	job, err := loadJob(path)
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

func (j Jobs) watchChanges(watcher *fsnotify.Watcher) {
	debounced := debounce.New(100 * time.Millisecond)

	for {
		select {

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			jobName := jobNameFromPath(event.Name)

			handleUpdate := func() {
				res, err := j.update(event.Name)
				if err != nil {
					logJobPrintf(jobName, "Error updating job: %v", err)
				}

				switch res {

				case jobsNoChanges:

				case jobsUpdated:
					logJobPrintf(jobName, "Updated job")

				case jobsAddedNew:
					logJobPrintf(jobName, "Added job")
				}
			}

			if filepath.Base(event.Name) == jobFileName {
				jobName := jobNameFromPath(event.Name)

				if event.Has(fsnotify.Create) {
					handleUpdate()
				} else if event.Has(fsnotify.Write) {
					debounced(handleUpdate)
				} else if event.Has(fsnotify.Remove) {
					j.mu.Lock()
					delete(j.byName, jobName)
					j.mu.Unlock()

					logJobPrintf(jobName, "Removed job")
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

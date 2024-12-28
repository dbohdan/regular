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
	"github.com/fsnotify/fsnotify"
	"github.com/mna/starstruct"
	"go.starlark.net/starlark"
)

const (
	dirName     = "regular"
	jobFileName = "job.star"

	enabledVar   = "enabled"
	shouldRunVar = "should_run"
)

func jobNameFromPath(path string) string {
	return filepath.Base(filepath.Dir(path))
}

func loadJob(path string) (Job, error) {
	thread := &starlark.Thread{Name: "job"}

	globals, err := starlark.ExecFile(thread, path, nil, nil)
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

type updateJobsResult int

const (
	jobsNoChanges updateJobsResult = iota
	jobsAddedNew
	jobsUpdated
)

func updateJobs(jobs map[string]Job, jobsMu sync.RWMutex, path string) (updateJobsResult, error) {
	jobName := jobNameFromPath(path)

	job, err := loadJob(path)
	if err != nil {
		return jobsNoChanges, fmt.Errorf("failed to load job: %v", err)
	}

	jobsMu.Lock()
	_, exists := jobs[jobName]
	jobs[jobName] = job
	jobsMu.Unlock()

	if exists {
		return jobsUpdated, nil
	}

	return jobsAddedNew, nil
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

func runJobs(jobs map[string]Job, jobsMu sync.RWMutex) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		thread := &starlark.Thread{Name: "scheduler"}

		jobsMu.RLock()
		defer jobsMu.RUnlock()

		for name, job := range jobs {
			if !job.Enabled {
				continue
			}

			args := starlark.Tuple{
				starlark.MakeInt(now.Year()),
				starlark.MakeInt(int(now.Month())),
				starlark.MakeInt(now.Day()),
				starlark.MakeInt(now.Hour()),
				starlark.MakeInt(now.Minute()),
				starlark.MakeInt(int(now.Weekday())),
			}

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
	}
}

func watchChanges(jobs map[string]Job, jobsMu sync.RWMutex, watcher *fsnotify.Watcher) {
	debounced := debounce.New(100 * time.Millisecond)

	for {
		select {

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			jobName := jobNameFromPath(event.Name)

			handleUpdate := func() {
				res, err := updateJobs(jobs, jobsMu, event.Name)
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
					jobsMu.Lock()
					delete(jobs, jobName)
					jobsMu.Unlock()

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

func main() {
	jobs := make(map[string]Job)
	var jobsMu sync.RWMutex

	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	config := Config{
		ConfigRoot: filepath.Join(xdg.ConfigHome, dirName),
		StateRoot:  filepath.Join(xdg.StateHome, dirName),
	}

	err = filepath.Walk(config.ConfigRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return watcher.Add(path)
		}

		if filepath.Base(path) == jobFileName {
			_, err := updateJobs(jobs, jobsMu, path)
			if err != nil {
				logJobPrintf(jobNameFromPath(path), "Error at startup: %v", err)
			}
		}

		return nil
	})

	go runJobs(jobs, jobsMu)
	go watchChanges(jobs, jobsMu, watcher)

	// Block forever.
	<-make(chan struct{})
}

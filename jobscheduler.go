package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bep/debounce"
	"github.com/fsnotify/fsnotify"

	"dbohdan.com/regular/envfile"
)

type jobScheduler struct {
	byName map[string]JobConfig

	mu *sync.RWMutex
}

type updateJobsResult int

const (
	jobsNoChanges updateJobsResult = iota
	jobsAddedNew
	jobsUpdated
)

func newJobScheduler() jobScheduler {
	return jobScheduler{
		byName: make(map[string]JobConfig),

		mu: &sync.RWMutex{},
	}
}

func (jsc jobScheduler) scheduleOnce(runner jobRunner) error {
	jsc.mu.RLock()
	defer jsc.mu.RUnlock()

	for name, job := range jsc.byName {
		err := job.schedule(runner)
		if err != nil {
			return newJobError(name, fmt.Errorf("scheduling error: %w", err))
		}
	}

	return nil
}

func (jsc jobScheduler) schedule(runner jobRunner) error {
	ticker := time.NewTicker(scheduleInterval)
	defer ticker.Stop()

	err := jsc.scheduleOnce(runner)
	if err != nil {
		return err
	}

	for range ticker.C {
		err := jsc.scheduleOnce(runner)
		if err != nil {
			return err
		}
	}

	return nil
}

func (jsc jobScheduler) update(configRoot, jobPath string) (updateJobsResult, *JobConfig, error) {
	jobDir := jobDir(jobPath)
	jobName := jobNameFromPath(jobPath)

	env := envfile.OS()
	globalEnvPath := filepath.Join(configRoot, globalEnvFileName)
	jobEnvPath := filepath.Join(jobDir, jobEnvFileName)

	for _, envItem := range []struct {
		name string
		path string
	}{
		{name: "global", path: globalEnvPath},
		{name: "job", path: jobEnvPath},
	} {
		newEnv, err := envfile.Load(envItem.path, true, env)
		if err != nil {
			return jobsNoChanges, nil, fmt.Errorf("failed to load %s env file: %v", envItem.name, err)
		}

		env = envfile.Merge(env, newEnv)
	}

	env[jobDirEnvVar] = jobDir

	job, err := loadJob(env, jobPath)
	if err != nil {
		return jobsNoChanges, nil, fmt.Errorf("failed to load job: %v", err)
	}

	jsc.mu.Lock()
	_, exists := jsc.byName[jobName]
	jsc.byName[jobName] = job
	jsc.mu.Unlock()

	if exists {
		return jobsUpdated, &job, nil
	}

	return jobsAddedNew, &job, nil
}

func (jsc jobScheduler) remove(name string) error {
	jsc.mu.Lock()
	defer jsc.mu.Unlock()

	_, exists := jsc.byName[name]
	if !exists {
		return fmt.Errorf("failed to find job to remove: %v", name)
	}

	delete(jsc.byName, name)
	return nil
}

func (jsc jobScheduler) watchChanges(configRoot string, watcher *fsnotify.Watcher) error {
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

				res, _, err := jsc.update(configRoot, updatePath)
				if err != nil {
					removeErr := jsc.remove(jobName)

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
					err := jsc.remove(jobName)
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

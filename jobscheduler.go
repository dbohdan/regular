package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/bep/debounce"
	"github.com/syncthing/notify"

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

func (jsc jobScheduler) addDueJobsToQueue(runner jobRunner, t time.Time) error {
	jsc.mu.RLock()
	defer jsc.mu.RUnlock()

	for name, job := range jsc.byName {
		err := job.addToQueueIfDue(runner, t)
		if err != nil {
			return newJobError(name, fmt.Errorf("scheduling error: %w", err))
		}
	}

	return nil
}

func (jsc jobScheduler) exists(name string) bool {
	jsc.mu.Lock()
	_, exists := jsc.byName[name]
	jsc.mu.Unlock()

	return exists
}

func (jsc jobScheduler) schedule(runner jobRunner) error {
	ticker := time.NewTicker(scheduleInterval)
	defer ticker.Stop()

	current := time.Now()
	var last time.Time

	err := jsc.addDueJobsToQueue(runner, current)
	if err != nil {
		return err
	}

	for range ticker.C {
		last = current
		current = time.Now()

		// Account for missed time.
		// Do not run missed jobs if more than maxMissedTime has elapsed.
		// On an overloaded system, the ticker can miss a minute.
		// For example, this may happen because Regular was swapped out.
		// It would prevent Regular from running jobs scheduled for that minute and that minute alone.
		// The purpose of this approach is to catch up on missed jobs.
		// However, we should run days' worth of missed jobs after system hibernation.
		if current.Sub(last) > maxMissedTime {
			last = current
		}

		for t := last; t.Before(current); t = t.Add(time.Minute) {
			err := jsc.addDueJobsToQueue(runner, t)
			if err != nil {
				return err
			}
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
		if err == nil {
			env = envfile.Merge(env, newEnv)
		} else if !os.IsNotExist(err) {
			return jobsNoChanges, nil, fmt.Errorf("failed to load %s env file: %v", envItem.name, err)
		}
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

func (jsc jobScheduler) watchChanges(configRoot string, eventChan <-chan notify.EventInfo) error {
	debounced := debounce.New(debounceInterval)

	for eventInfo := range eventChan {
		event := eventInfo.Event()
		eventPath := eventInfo.Path()

		basename := filepath.Base(eventPath)
		jobName := jobNameFromPath(eventPath)
		jobConfigPath := path.Join(configRoot, jobName, jobConfigFileName)

		handleUpdate := func() {
			res, _, err := jsc.update(configRoot, jobConfigPath)
			if err != nil {
				// If the file doesn't exist or there is another error, remove the job.
				removeErr := jsc.remove(jobName)
				if removeErr == nil {
					if os.IsNotExist(err) {
						logJobPrintf(jobName, "Removed job because config file is gone")
					} else {
						logJobPrintf(jobName, "Removed job after update error: %v", err)
					}
				} else {
					// Log both errors if removal fails.
					logJobPrintf(jobName, "Failed to remove job: %v (original error: %v)", removeErr, err)
				}

				// Do not proceed after handling an error or job removal.
				return
			}

			switch res {

			case jobsNoChanges:
				// This case might not happen often with file events, but log just in case.
				logJobPrintf(jobName, "Job checked; no effective changes detected")

			case jobsUpdated:
				logJobPrintf(jobName, "Updated job")

			case jobsAddedNew:
				logJobPrintf(jobName, "Added job")
			}
		}

		if basename == jobConfigFileName {
			// Debounce updates to handle rapid saves.
			if _, err := os.Stat(eventPath); err == nil {
				debounced(handleUpdate)
			} else if os.IsNotExist(err) {
				// If the file doesn't exist by the time debounce runs, treat as removal
				errRemove := jsc.remove(jobName)
				if errRemove == nil {
					logJobPrintf(jobName, "Removed job because config file is gone")
				} else {
					logJobPrintf(jobName, "Failed to remove job with config file gone: %v", errRemove)
				}
			} else {
				logJobPrintf(jobName, "Error calling os.Stat on file %q before update: %v", eventPath, err)
			}
		} else if basename == jobEnvFileName && jsc.exists(jobName) {
			debounced(handleUpdate)
		} else if event == notify.Create {
			// Handle creation of other files or dirs.
			// If a directory is created, check if it contains a job config file.
			if info, err := os.Stat(eventPath); err == nil && info.IsDir() {
				if _, err := os.Stat(jobConfigPath); err == nil {
					debounced(handleUpdate)
				}
			}
		}
	}

	return nil
}

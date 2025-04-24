package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
	"github.com/syncthing/notify"
)

func (r *StartCmd) Run(config Config) error {
	withLog(func() error {
		return runService(config)
	})

	return nil
}

func runService(config Config) error {
	lockPath := filepath.Join(config.StateRoot, appLockFileName)
	fileLock := flock.New(lockPath)

	log.Print("Starting")

	locked, err := fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("error checking lock file: %w", err)
	}
	if !locked {
		return fmt.Errorf("another instance is already running")
	}
	defer func() {
		_ = fileLock.Unlock()
	}()

	jobs := newJobScheduler()

	eventChan := make(chan notify.EventInfo, 1)

	// "..." indicates recursive watching.
	watchPath := filepath.Join(config.ConfigRoot, "...")
	if err := notify.Watch(watchPath, eventChan, notify.Create, notify.Rename, notify.Remove, notify.Write); err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer notify.Stop(eventChan)

	loadedJobs := []string{}
	err = filepath.Walk(config.ConfigRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Base(path) == jobConfigFileName {
			jobName := jobNameFromPath(path)
			_, _, err := jobs.update(config.ConfigRoot, path)
			if err == nil {
				loadedJobs = append(loadedJobs, jobName)
			} else {
				logJobPrintf(jobName, "Error at startup: %v", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("error looking for jobs in config dir: %w", err)
	}
	log.Print("Loaded jobs: " + strings.Join(loadedJobs, ", "))

	db, err := openAppDB(config.StateRoot)
	if err != nil {
		return err
	}
	defer db.close()
	runner, _ := newJobRunner(db, notifyUserByEmail, config.StateRoot)

	go withLog(func() error {
		return jobs.schedule(runner)
	})
	go withLog(func() error {
		return jobs.watchChanges(config.ConfigRoot, eventChan)
	})
	go runner.run()

	// Block forever.
	<-make(chan struct{})
	return nil
}

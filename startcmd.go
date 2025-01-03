package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/gofrs/flock"
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

	locked, err := fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("error checking lock file: %w", err)
	}
	if !locked {
		return fmt.Errorf("another instance is already running")
	}
	defer fileLock.Unlock()

	jobs := newJobScheduler()

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
			_, _, err := jobs.update(config.ConfigRoot, path)
			if err != nil {
				logJobPrintf(jobNameFromPath(path), "Error at startup: %v", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("error looking for jobs in config dir: %w", err)
	}

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
		return jobs.watchChanges(config.ConfigRoot, watcher)
	})
	go runner.run()

	// Block forever.
	<-make(chan struct{})
	return nil
}

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

func (r *RunCmd) Run(config Config) error {
	return runService(config)
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

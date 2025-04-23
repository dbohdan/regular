package main

import (
	"fmt"
	"path/filepath"
	"time"
)

func (r *RunCmd) Run(config Config) error {
	db, err := openAppDB(config.StateRoot)
	if err != nil {
		return err
	}
	defer db.close()

	runner, err := newJobRunner(db, notifyUserByEmail, config.StateRoot)
	if err != nil {
		return err
	}

	jobs := newJobScheduler()
	now := time.Now()

	for _, jobName := range r.JobNames {
		path := filepath.Join(config.ConfigRoot, jobName, jobConfigFileName)

		_, job, err := jobs.update(config.ConfigRoot, path)
		if err != nil {
			logJobPrintf(jobNameFromPath(path), "Error loading job: %v", err)
			return nil
		}

		// Either force-run or check should_run.
		if r.Force {
			runner.addJob(*job)
		} else {
			if err := job.addToQueueIfDue(runner, now); err != nil {
				return fmt.Errorf("failed to schedule job %q: %w", job.Name, err)
			}
		}
	}

	// Run all queued jobs.
	for queueName := range runner.queues {
		if err := runner.runQueueHead(queueName); err != nil {
			return err
		}
	}

	return nil
}

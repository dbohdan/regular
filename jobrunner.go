package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dbohdan.com/regular/envfile"
)

type jobRunner struct {
	db        *appDB
	notify    notifyWhenDone
	queues    map[string]jobQueue
	stateRoot string

	mu *sync.Mutex
}

func newJobRunner(db *appDB, notify notifyWhenDone, stateRoot string) (jobRunner, error) {
	return jobRunner{
		db:        db,
		notify:    notify,
		queues:    make(map[string]jobQueue),
		stateRoot: stateRoot,
		mu:        &sync.Mutex{},
	}, nil
}

func (r jobRunner) lastCompleted(jobName string) (*CompletedJob, error) {
	completed, err := r.db.getLastCompleted(jobName)
	if err != nil {
		return nil, fmt.Errorf("failed to get last completed job for %q: %w", jobName, err)
	}

	return completed, nil
}

func (r jobRunner) addJob(job JobConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	queueName := job.QueueName()

	queue, ok := r.queues[queueName]
	if !ok {
		queue = newJobQueue()
		r.queues[queueName] = queue
	}

	if !job.Duplicate {
		for _, otherJob := range queue.jobs {
			if otherJob.Name == job.Name {
				return
			}
		}
	}

	queue.jobs = append(queue.jobs, job)
	r.queues[queueName] = queue

	// Report the queue length before the job was added.
	if len(queue.jobs) == 1 {
		logJobPrintf(
			job.Name,
			"Put job in empty runner queue: %v",
			queueName,
		)
	} else {
		logJobPrintf(
			job.Name,
			"Put job in runner queue of length %v: %v",
			len(queue.jobs)-1,
			queueName,
		)
	}
}

func (r jobRunner) activateQueueHead(queueName string) (*JobConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	queue, ok := r.queues[queueName]

	if !ok {
		return nil, fmt.Errorf("requested to run head of nonexistent queue: %v", queueName)
	}

	if queue.activeJob || len(queue.jobs) == 0 {
		return nil, nil
	}

	job := queue.jobs[0]

	queue.activeJob = true
	r.queues[queueName] = queue

	return &job, nil
}

func (r jobRunner) runQueueHead(queueName string) error {
	job, err := r.activateQueueHead(queueName)
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}

	jobStateDir := filepath.Join(r.stateRoot, job.Name)

	if job.Jitter > 0 {
		sleepDuration := time.Duration(job.Jitter.Seconds()*rand.Float64()) * time.Second
		logJobPrintf(job.Name, "Waiting %v before start", formatDuration(sleepDuration))

		time.Sleep(sleepDuration)
	}

	cj := CompletedJob{}
	cj.Started = time.Now()
	logJobPrintf(job.Name, "Started")

	stdoutFilePath := filepath.Join(jobStateDir, stdoutFileName)
	stderrFilePath := filepath.Join(jobStateDir, stderrFileName)

	var stdoutFile io.Writer
	var stderrFile io.Writer
	if job.Log {
		if err := os.MkdirAll(jobStateDir, dirPerms); err != nil {
			return newJobError(job.Name, fmt.Errorf("failed to create job state directory: %w", err))
		}

		var err error
		stdoutFile, err = os.OpenFile(
			stdoutFilePath,
			os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
			filePerms,
		)
		if err != nil {
			return newJobError(job.Name, fmt.Errorf("failed to create stdout log file: %w", err))
		}

		stderrFile, err = os.OpenFile(
			stderrFilePath,
			os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
			filePerms,
		)
		if err != nil {
			return newJobError(job.Name, fmt.Errorf("failed to create stderr log file: %w", err))
		}
	}

	jobDir := job.Env[jobDirEnvVar]

	runErr := runCommand(job.Name, job.Env, jobDir, job.Command, nil, stdoutFile, stderrFile)
	cj.Error = ""
	if runErr != nil {
		cj.Error = runErr.Error()
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		cj.ExitStatus = exitErr.ExitCode()
	}

	logJobPrintf(job.Name, "Finished")
	cj.Finished = time.Now()

	r.mu.Lock()
	queue, ok := r.queues[queueName]
	if ok {
		queue.activeJob = false
		queue.jobs = queue.jobs[1:]
		r.queues[queueName] = queue
	}

	saveErr := r.db.saveCompletedJob(job.Name, cj, []logFile{
		{name: "stdout", path: stdoutFilePath},
		{name: "stderr", path: stderrFilePath},
	})
	notifyErr := notifyIfNeeded(r.notify, job.Notify, job.Name, cj)
	r.mu.Unlock()

	if notifyErr != nil {
		return newJobError(job.Name, fmt.Errorf("failed to notify about completed job: %w", notifyErr))
	}

	if saveErr != nil {
		return newJobError(job.Name, fmt.Errorf("failed to save completed job: %w", saveErr))
	}

	if runErr != nil {
		return newJobError(job.Name, fmt.Errorf("command failed: %w", runErr))
	}

	return nil
}

func (r jobRunner) run() {
	ticker := time.NewTicker(runInterval)
	defer ticker.Stop()

	for range ticker.C {
		names := []string{}

		r.mu.Lock()
		for queueName, _ := range r.queues {
			names = append(names, queueName)
		}
		r.mu.Unlock()

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

func runCommand(jobName string, env envfile.Env, dir string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(cmd) == 0 {
		return fmt.Errorf("empty command")
	}

	c := exec.CommandContext(context.Background(), cmd[0], cmd[1:]...)
	c.Dir = dir
	c.Env = env.Strings()
	c.Stdin = stdin
	c.Stdout = stdout
	c.Stderr = stderr

	return c.Run()
}

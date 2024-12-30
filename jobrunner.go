package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	shsyntax "mvdan.cc/sh/v3/syntax"
)

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

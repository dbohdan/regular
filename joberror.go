package main

import "fmt"

// Wraps an error with a job name.
type JobError struct {
	JobName string
	Err     error
}

func (e *JobError) Error() string {
	return fmt.Sprintf("job %q: %v", e.JobName, e.Err)
}

func newJobError(jobName string, err error) *JobError {
	return &JobError{JobName: jobName, Err: err}
}

package main

import (
	"time"
)

type CompletedJob struct {
	Error      string
	ExitStatus int
	Started    time.Time
	Finished   time.Time
}

func (cj CompletedJob) IsSuccess() bool {
	return cj.ExitStatus == 0 && cj.Error == ""
}

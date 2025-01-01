package main

import (
	"encoding/json"
	"time"
)

type CompletedJob struct {
	Error      string    `json:"error"`
	ExitStatus int       `json:"exit_status"`
	Started    time.Time `json:"started"`
	Finished   time.Time `json:"finished"`
	StdoutFile string    `json:"stdout"`
	StderrFile string    `json:"stderr"`
}

func (cj CompletedJob) IsSuccess() bool {
	return cj.ExitStatus == 0 && cj.Error == ""
}

func (cj CompletedJob) MarshalJSON() ([]byte, error) {
	type Alias CompletedJob

	return json.Marshal(&struct {
		Started  string `json:"started"`
		Finished string `json:"finished"`
		*Alias
	}{
		Started:  cj.Started.Format(time.RFC3339),
		Finished: cj.Finished.Format(time.RFC3339),
		Alias:    (*Alias)(&cj),
	})
}

func UnmarshalCompletedJob(data []byte) (CompletedJob, error) {
	type Alias CompletedJob
	var cj CompletedJob

	stringTimes := &struct {
		Started  string `json:"started"`
		Finished string `json:"finished"`
		*Alias
	}{
		Alias: (*Alias)(&cj),
	}

	var err error
	if err = json.Unmarshal(data, &stringTimes); err != nil {
		return cj, err
	}

	cj.Started, err = time.Parse(time.RFC3339, stringTimes.Started)
	if err != nil {
		return cj, err
	}

	cj.Finished, err = time.Parse(time.RFC3339, stringTimes.Finished)
	if err != nil {
		return cj, err
	}

	return cj, nil
}

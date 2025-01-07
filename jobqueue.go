package main

type jobQueue struct {
	activeJob bool
	jobs      []JobConfig
}

func newJobQueue() jobQueue {
	return jobQueue{
		jobs: []JobConfig{},
	}
}

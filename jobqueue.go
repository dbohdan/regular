package main

import "sync"

type jobQueue struct {
	activeJob bool
	jobs      []JobConfig

	mu *sync.RWMutex
}

func newJobQueue() jobQueue {
	return jobQueue{
		jobs: []JobConfig{},

		mu: &sync.RWMutex{},
	}
}

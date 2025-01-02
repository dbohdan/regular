package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func (l *ListCmd) Run(config Config) error {
	entries, err := os.ReadDir(config.ConfigRoot)
	if err != nil {
		return fmt.Errorf("failed to read config directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		jobFile := filepath.Join(config.ConfigRoot, entry.Name(), jobFileName)
		if _, err := os.Stat(jobFile); err == nil {
			fmt.Println(entry.Name())
		}
	}

	return nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nxadm/tail"
)

func (l *LogCmd) Run(config Config) error {
	logPath := filepath.Join(config.StateRoot, appLogFileName)
	lines, err := tailFile(logPath, l.LogLines)

	if err != nil {
		return fmt.Errorf("error reading log file: %w", err)
	}

	if len(lines) == 0 {
		fmt.Println("Log is empty")
		return nil
	}

	for _, line := range lines {
		fmt.Println(line)
	}

	return nil
}

func tailFile(path string, maxLines int) ([]string, error) {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	t, err := tail.TailFile(
		path,
		tail.Config{
			Follow:   false,
			Location: nil,
		},
	)
	if err != nil {
		return nil, err
	}
	defer t.Stop()

	// Collect the lines in a ring buffer.
	lines := []string{}
	for line := range t.Lines {
		lines = append(lines, line.Text)

		if len(lines) > maxLines {
			lines = lines[1:]
		}
	}

	return lines, nil
}

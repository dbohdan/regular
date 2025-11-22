package main

import (
	"fmt"
	"os"
)

// createDirectories creates the ConfigRoot and StateRoot directories if they don't exist.
func createDirectories(config Config) error {
	if err := os.MkdirAll(config.ConfigRoot, dirPerms); err != nil {
		return fmt.Errorf("failed to create config directory %q: %w", config.ConfigRoot, err)
	}

	if err := os.MkdirAll(config.StateRoot, dirPerms); err != nil {
		return fmt.Errorf("failed to create state directory %q: %w", config.StateRoot, err)
	}

	return nil
}

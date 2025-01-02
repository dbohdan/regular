package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/term"

	"dbohdan.com/regular/envfile"
)

func (s *StatusCmd) Run(config Config) error {
	width := getTermWidth()
	separator := strings.Repeat("-", width)

	jobs := newJobScheduler()

	err := filepath.Walk(config.ConfigRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Base(path) == jobFileName {
			_, _, err := jobs.update(config.ConfigRoot, path)
			if err != nil {
				return fmt.Errorf("error loading job %q: %w", path, err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	db, err := openAppDB(config.StateRoot)
	if err != nil {
		return err
	}
	defer db.close()

	secret := regexp.MustCompile(secretRegexp)

	seenNames := make(map[string]struct{})

	for _, name := range s.JobNames {
		job, ok := jobs.byName[name]
		if !ok {
			continue
		}

		_, seen := seenNames[name]
		if seen {
			continue
		}
		seenNames[name] = struct{}{}

		for key, value := range envfile.OS() {
			if osValue, ok := job.Env[key]; ok && value == osValue {
				delete(job.Env, key)
				continue
			}

			if secret.MatchString(key) {
				job.Env[key] = redactedValue
			}
		}

		fmt.Println(name)
		fmt.Println("    dir:", filepath.Join(config.ConfigRoot, name))

		if len(job.Env) == 0 {
			fmt.Println("    env: none")
		} else {
			fmt.Println("    env:")
			for k, v := range job.Env {
				fmt.Printf("        %v: %v\n", k, v)
			}
		}

		fmt.Println("    enabled:", map[bool]string{true: "yes", false: "no"}[job.Enabled])
		fmt.Println("    jitter:", formatDuration(job.Jitter))
		fmt.Println("    queue:", job.QueueName())

		completed, err := db.getLastCompleted(job.Name)
		if err != nil {
			return fmt.Errorf("error getting last completed job %q: %w", name, err)
		}

		if completed == nil {
			fmt.Println("    last start: unknown")
			fmt.Println("    run time: unknown")
			fmt.Println("    exit status: unknown")
		} else {
			fmt.Println("    last start:", completed.Started.Format(timestampFormat))
			fmt.Println("    run time:", completed.Finished.Sub(completed.Started).Round(time.Second))
			fmt.Println("    exit status:", completed.ExitStatus)
		}

		fmt.Println("    logs:")

		stdoutLines, err := db.getJobLogs(name, "stdout", s.LogLines)
		if err != nil {
			return fmt.Errorf("error loading stdout for job %q: %w", name, err)
		}
		if len(stdoutLines) == 0 {
			fmt.Println("        stdout: empty")
		} else {
			fmt.Println("        stdout:")
			fmt.Println(separator)
			for _, line := range stdoutLines {
				fmt.Println(line)
			}
			fmt.Println(separator)
		}

		stderrLines, err := db.getJobLogs(name, "stderr", s.LogLines)
		if err != nil {
			return fmt.Errorf("error loading stderr for job %q: %w", name, err)
		}
		if len(stderrLines) == 0 {
			fmt.Println("        stderr: empty")
		} else {
			fmt.Println("        stderr:")
			fmt.Println(separator)
			for _, line := range stderrLines {
				fmt.Println(line)
			}
			fmt.Println(separator)
		}

		fmt.Println()
	}

	return nil
}

func getTermWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		return w
	}

	return 80
}

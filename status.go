package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nxadm/tail"
	"golang.org/x/term"

	"dbohdan.com/regular/envfile"
)

func (s *StatusCmd) Run(config Config) error {
	width := getTermWidth()
	separator := strings.Repeat("-", width)

	jobs := newJobStore()

	err := filepath.Walk(config.ConfigRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Base(path) == jobFileName {
			_, err := jobs.update(config.ConfigRoot, path)
			if err != nil {
				return fmt.Errorf("error loading job %s: %w", path, err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	secret := regexp.MustCompile(secretRegexp)

	for name, job := range jobs.byName {
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
				fmt.Printf("        %s: %s\n", k, v)
			}
		}

		fmt.Println("    enabled:", map[bool]string{true: "yes", false: "no"}[job.Enabled])
		fmt.Println("    jitter:", map[time.Duration]string{0: "none"}[job.Jitter])
		fmt.Println("    queue:", job.Queue)

		completed, err := readCompletedJob(config.StateRoot, name)
		if err != nil {
			return fmt.Errorf("error reading completed job %s: %w", name, err)
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

		stdoutPath := filepath.Join(config.StateRoot, name, stdoutFileName)
		stderrPath := filepath.Join(config.StateRoot, name, stderrFileName)

		stdoutTime, stdoutLines, err := tailLog(stdoutPath, s.LogLines)
		if err != nil {
			return fmt.Errorf("error reading stdout for %q: %w", name, err)
		}

		stderrTime, stderrLines, err := tailLog(stderrPath, s.LogLines)
		if err != nil {
			return fmt.Errorf("error reading stderr for %q: %w", name, err)
		}

		fmt.Println("        stdout:")
		if !stdoutTime.IsZero() {
			fmt.Println("            modified:", stdoutTime.Format(timestampFormat))
			if len(stdoutLines) == 0 {
				fmt.Println("            lines: none")
			} else {
				fmt.Println("            lines:")
				fmt.Println(separator)
				for _, line := range stdoutLines {
					fmt.Println(line)
				}
				fmt.Println(separator)
			}
		}

		fmt.Println("        stderr:")
		if !stderrTime.IsZero() {
			fmt.Println("            modified:", stderrTime.Format(timestampFormat))
			if len(stderrLines) == 0 {
				fmt.Println("            lines: none")
			} else {
				fmt.Println("            lines:")
				fmt.Println(separator)
				for _, line := range stderrLines {
					fmt.Println(line)
				}
				fmt.Println(separator)
			}
		}

		fmt.Println()
	}

	return nil
}

func readCompletedJob(stateRoot, jobName string) (*CompletedJob, error) {
	completedPath := filepath.Join(stateRoot, jobName, completedJobFileName)

	data, err := os.ReadFile(completedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	completed, err := UnmarshalCompletedJob(data)
	if err != nil {
		return nil, err
	}

	return &completed, nil
}

func tailLog(path string, maxLines int) (time.Time, []string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil, nil
		}

		return time.Time{}, nil, err
	}

	t, err := tail.TailFile(
		path,
		tail.Config{
			Follow:   false,
			Location: nil,
		},
	)
	if err != nil {
		return time.Time{}, nil, err
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

	return info.ModTime(), lines, nil
}

func getTermWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		return w
	}

	return 80
}

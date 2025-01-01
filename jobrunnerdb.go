package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type jobRunnerDB struct {
	db *sql.DB
}

func openJobRunnerDB(stateRoot string) (*jobRunnerDB, error) {
	if err := os.MkdirAll(stateRoot, dirPerms); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %v", err)
	}

	dbPath := filepath.Join(stateRoot, jobRunnerDBFileName)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &jobRunnerDB{db: db}, nil
}

func (c *jobRunnerDB) close() error {
	return c.db.Close()
}

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS completed_jobs (
			id INTEGER PRIMARY KEY,
			job_name TEXT NOT NULL,
			error TEXT,
			exit_status INTEGER NOT NULL,
			started DATETIME NOT NULL,
			finished DATETIME NOT NULL,
			stdout_file TEXT NOT NULL,
			stderr_file TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_completed_jobs_job_name ON completed_jobs(job_name);
	`)

	return err
}

func (c *jobRunnerDB) saveCompletedJob(jobName string, completed CompletedJob) error {
	_, err := c.db.Exec(`
		INSERT INTO completed_jobs (
			job_name,
			error,
			exit_status,
			started,
			finished,
			stdout_file,
			stderr_file
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		jobName,
		completed.Error,
		completed.ExitStatus,
		completed.Started,
		completed.Finished,
		completed.StdoutFile,
		completed.StderrFile,
	)

	return err
}

func (c *jobRunnerDB) getLastCompleted(jobName string) (*CompletedJob, error) {
	var completed CompletedJob
	err := c.db.QueryRow(`
		SELECT
			error,
			exit_status,
			started,
			finished,
			stdout_file,
			stderr_file
		FROM completed_jobs
		WHERE job_name = ?
		ORDER BY id DESC LIMIT 1`,
		jobName,
	).Scan(
		&completed.Error,
		&completed.ExitStatus,
		&completed.Started,
		&completed.Finished,
		&completed.StdoutFile,
		&completed.StderrFile,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &completed, nil
}

func (c *jobRunnerDB) getAllCompleted(jobName string) ([]CompletedJob, error) {
	rows, err := c.db.Query(`
		SELECT
			error,
			exit_status,
			started,
			finished,
			stdout_file,
			stderr_file
		FROM completed_jobs
		WHERE job_name = ?
		ORDER BY id ASC`,
		jobName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var completed []CompletedJob
	for rows.Next() {
		var job CompletedJob
		err := rows.Scan(
			&job.Error,
			&job.ExitStatus,
			&job.Started,
			&job.Finished,
			&job.StdoutFile,
			&job.StderrFile,
		)
		if err != nil {
			return nil, err
		}

		completed = append(completed, job)
	}

	return completed, rows.Err()
}

package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type appDB struct {
	db *sql.DB
}

func openAppDB(stateRoot string) (*appDB, error) {
	if err := os.MkdirAll(stateRoot, dirPerms); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %v", err)
	}

	dbPath := filepath.Join(stateRoot, appDBFileName)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	if err := createSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &appDB{db: db}, nil
}

func (c *appDB) close() error {
	return c.db.Close()
}

func createSchema(db *sql.DB) error {
	_, err := db.Exec(`
	    PRAGMA foreign_keys=ON;

		CREATE TABLE IF NOT EXISTS completed_jobs (
			id INTEGER PRIMARY KEY,
			job_name TEXT NOT NULL,
			error TEXT,
			exit_status INTEGER NOT NULL,
			started DATETIME NOT NULL,
			finished DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_completed_jobs_job_name ON completed_jobs(job_name);

		CREATE TABLE IF NOT EXISTS job_logs (
			id INTEGER PRIMARY KEY,
			completed_job_id INTEGER NOT NULL,
			log_name TEXT NOT NULL,
			line_number INTEGER NOT NULL,
			line TEXT NOT NULL,
			FOREIGN KEY(completed_job_id) REFERENCES completed_jobs(id)
		);

		CREATE INDEX IF NOT EXISTS idx_job_logs_completed_job_id ON job_logs(completed_job_id);
	`)

	return err
}

func (c *appDB) saveCompletedJob(jobName string, completed CompletedJob, logs []logFile) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	result, err := tx.Exec(`
		INSERT INTO completed_jobs (
			job_name,
			error,
			exit_status,
			started,
			finished
		) VALUES (?, ?, ?, ?, ?)`,
		jobName,
		completed.Error,
		completed.ExitStatus,
		completed.Started,
		completed.Finished,
	)
	if err != nil {
		return err
	}

	jobID, err := result.LastInsertId()
	if err != nil {
		return err
	}

	for _, logFile := range logs {
		if err := c.saveLogFile(tx, jobID, logFile.name, logFile.path); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (c *appDB) saveLogFile(tx *sql.Tx, jobID int64, logName, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	buf := make([]byte, maxLogBufferSize)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return err
	}
	buf = buf[:n]

	lineNum := 1
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	for scanner.Scan() {
		_, err = tx.Exec(`
			INSERT INTO job_logs (
				completed_job_id,
				log_name,
				line_number,
				line
			) VALUES (?, ?, ?, ?)`,
			jobID,
			logName,
			lineNum,
			scanner.Text(),
		)
		if err != nil {
			return err
		}
		lineNum++
	}
	return scanner.Err()
}

func (c *appDB) getLastCompleted(jobName string) (*CompletedJob, error) {
	var completed CompletedJob
	err := c.db.QueryRow(`
		SELECT
			error,
			exit_status,
			started,
			finished
		FROM completed_jobs
		WHERE job_name = ?
		ORDER BY id DESC LIMIT 1`,
		jobName,
	).Scan(
		&completed.Error,
		&completed.ExitStatus,
		&completed.Started,
		&completed.Finished,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &completed, nil
}

func (c *appDB) getJobLogs(jobName string, logName string, limit int) ([]string, error) {
	rows, err := c.db.Query(`
		SELECT line
		FROM (
			SELECT l.line, l.line_number
			FROM job_logs l
			JOIN completed_jobs j ON j.id = l.completed_job_id
			WHERE l.log_name = ?
			AND j.id = (
				SELECT id
				FROM completed_jobs
				WHERE job_name = ?
				ORDER BY id DESC
				LIMIT 1
			)
			ORDER BY l.line_number DESC
			LIMIT ?
		)
		ORDER BY line_number ASC`,
		logName,
		jobName,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}

		lines = append(lines, line)
	}

	return lines, rows.Err()
}

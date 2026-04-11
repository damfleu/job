package db

import (
	"fmt"

	"job/internal/model"
)

func (d *DB) ListActive() ([]*model.Job, error) {
	rows, err := d.db.Query(
		`SELECT `+jobCols+` FROM jobs WHERE status != 'completed' ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("db: list active: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) ListCompleted(limit int) ([]*model.Job, error) {
	rows, err := d.db.Query(
		`SELECT `+jobCols+` FROM jobs WHERE status = 'completed' ORDER BY stopped_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("db: list completed: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) Search(query string) ([]*model.Job, error) {
	rows, err := d.db.Query(
		`SELECT `+jobCols+` FROM jobs WHERE command LIKE ? ORDER BY created_at DESC`,
		"%"+query+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("db: search: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func scanJobs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*model.Job, error) {
	var jobs []*model.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

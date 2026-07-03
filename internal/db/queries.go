package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"job/internal/model"
)

func (d *DB) ListActive(filter, context string) ([]*model.Job, error) {
	query := `SELECT ` + jobCols + ` FROM jobs WHERE status != 'completed'`
	args := []any{}
	if filter != "" {
		query += ` AND cmd_str(command) REGEXP ?`
		args = append(args, filter)
	}
	if context != "" {
		query += ` AND context = ?`
		args = append(args, context)
	}
	query += ` ORDER BY created_at`
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list active: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) ListCompleted(limit int, filter, context string) ([]*model.Job, error) {
	query := `SELECT ` + jobCols + ` FROM jobs WHERE status = 'completed'`
	args := []any{}
	if filter != "" {
		query += ` AND cmd_str(command) REGEXP ?`
		args = append(args, filter)
	}
	if context != "" {
		query += ` AND context = ?`
		args = append(args, context)
	}
	query += ` ORDER BY stopped_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list completed: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) GetByKeys(keys []string) ([]*model.Job, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(keys))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	rows, err := d.db.Query(
		`SELECT `+jobCols+` FROM jobs WHERE key IN (`+placeholders+`) ORDER BY created_at`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("db: get by keys: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) ListCompletedBefore(t time.Time, context string) ([]*model.Job, error) {
	query := `SELECT ` + jobCols + ` FROM jobs WHERE status = 'completed' AND stopped_at < ?`
	args := []any{t.UTC().Format(time.RFC3339Nano)}
	if context != "" {
		query += ` AND context = ?`
		args = append(args, context)
	}
	query += ` ORDER BY stopped_at`
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list completed before: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) GetLastKeyForContext(context string) (string, error) {
	var key string
	err := d.db.QueryRow(
		`SELECT key FROM jobs WHERE context = ? AND automated = 0 ORDER BY created_at DESC LIMIT 1`,
		context,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("db: get last key for context %q: %w", context, err)
	}
	return key, nil
}

// searchLimit caps the number of rows Search returns, most-recently-created first.
// An empty query matches every job (LIKE "%%"), so this also bounds the
// picker's bare-"." candidate list.
const searchLimit = 50

func (d *DB) Search(query, context string) ([]*model.Job, error) {
	sqlQuery := `SELECT ` + jobCols + ` FROM jobs WHERE command LIKE ?`
	args := []any{"%" + query + "%"}
	if context != "" {
		sqlQuery += ` AND context = ?`
		args = append(args, context)
	}
	sqlQuery += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, searchLimit)
	rows, err := d.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("db: search: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (d *DB) FindByAlias(alias string) (*model.Job, error) {
	row := d.db.QueryRow(`SELECT `+jobCols+` FROM jobs WHERE alias = ? ORDER BY created_at DESC LIMIT 1`, alias)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db: find by alias %q: %w", alias, err)
	}
	return job, nil
}

func (d *DB) FindByKeyPrefix(prefix string) ([]*model.Job, error) {
	rows, err := d.db.Query(
		`SELECT `+jobCols+` FROM jobs WHERE key LIKE ? ORDER BY created_at DESC`,
		prefix+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("db: find by prefix %q: %w", prefix, err)
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

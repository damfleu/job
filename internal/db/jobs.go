package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"job/internal/model"
)

// jobCols is the canonical column order used in all SELECT queries.
const jobCols = `key, alias, command, work_dir, log_file, status, reason,
    exit_code, pid, pgid, hostname, username, created_at, started_at, stopped_at, deps`

func (d *DB) Insert(job *model.Job) error {
	cmd, err := json.Marshal(job.Command)
	if err != nil {
		return fmt.Errorf("db: marshal command: %w", err)
	}
	deps, err := marshalDeps(job.Deps)
	if err != nil {
		return err
	}

	_, err = d.db.Exec(`
		INSERT INTO jobs
		  (key, alias, command, work_dir, log_file, status, reason, exit_code,
		   pid, pgid, hostname, username, created_at, started_at, stopped_at, deps)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		job.Key,
		nullStr(job.Alias),
		string(cmd),
		job.WorkDir,
		job.LogFile,
		string(job.Status),
		nullStr(string(job.Reason)),
		job.ExitCode,
		job.PID,
		job.PGID,
		nullStr(job.Hostname),
		nullStr(job.Username),
		job.CreatedAt.UTC().Format(time.RFC3339Nano),
		nullTime(job.StartedAt),
		nullTime(job.StoppedAt),
		deps,
	)
	if err != nil {
		return fmt.Errorf("db: insert %s: %w", job.Key, err)
	}
	return nil
}

func (d *DB) Get(key string) (*model.Job, error) {
	row := d.db.QueryRow(`SELECT `+jobCols+` FROM jobs WHERE key = ?`, key)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db: get %s: %w", key, err)
	}
	return job, nil
}

func (d *DB) Update(job *model.Job) error {
	cmd, err := json.Marshal(job.Command)
	if err != nil {
		return fmt.Errorf("db: marshal command: %w", err)
	}
	deps, err := marshalDeps(job.Deps)
	if err != nil {
		return err
	}

	res, err := d.db.Exec(`
		UPDATE jobs SET
		  alias=?, command=?, work_dir=?, log_file=?,
		  status=?, reason=?, exit_code=?,
		  pid=?, pgid=?,
		  hostname=?, username=?,
		  started_at=?, stopped_at=?,
		  deps=?
		WHERE key=?`,
		nullStr(job.Alias),
		string(cmd),
		job.WorkDir,
		job.LogFile,
		string(job.Status),
		nullStr(string(job.Reason)),
		job.ExitCode,
		job.PID,
		job.PGID,
		nullStr(job.Hostname),
		nullStr(job.Username),
		nullTime(job.StartedAt),
		nullTime(job.StoppedAt),
		deps,
		job.Key,
	)
	if err != nil {
		return fmt.Errorf("db: update %s: %w", job.Key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) Delete(key string) error {
	res, err := d.db.Exec(`DELETE FROM jobs WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("db: delete %s: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) GetLastKey() (string, error) {
	var key string
	err := d.db.QueryRow(`SELECT value FROM metadata WHERE key = 'last_key'`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("db: get last key: %w", err)
	}
	return key, nil
}

func (d *DB) SetLastKey(key string) error {
	_, err := d.db.Exec(
		`INSERT INTO metadata(key, value) VALUES('last_key', ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key,
	)
	if err != nil {
		return fmt.Errorf("db: set last key: %w", err)
	}
	return nil
}

// scanJob scans a single row into a Job.
func scanJob(s interface{ Scan(...any) error }) (*model.Job, error) {
	var (
		j         model.Job
		alias     sql.NullString
		reason    sql.NullString
		exitCode  sql.NullInt64
		hostname  sql.NullString
		username  sql.NullString
		startedAt sql.NullString
		stoppedAt sql.NullString
		status    string
		createdAt string
		cmdJSON   string
		depsJSON  sql.NullString
	)

	err := s.Scan(
		&j.Key, &alias, &cmdJSON, &j.WorkDir, &j.LogFile,
		&status, &reason, &exitCode,
		&j.PID, &j.PGID, &hostname, &username,
		&createdAt, &startedAt, &stoppedAt, &depsJSON,
	)
	if err != nil {
		return nil, err
	}

	j.Alias = alias.String
	j.Status = model.Status(status)
	j.Reason = model.Reason(reason.String)
	j.Hostname = hostname.String
	j.Username = username.String

	if exitCode.Valid {
		n := int(exitCode.Int64)
		j.ExitCode = &n
	}

	j.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}

	if startedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, startedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse started_at: %w", err)
		}
		j.StartedAt = &t
	}

	if stoppedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, stoppedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse stopped_at: %w", err)
		}
		j.StoppedAt = &t
	}

	if err := json.Unmarshal([]byte(cmdJSON), &j.Command); err != nil {
		return nil, fmt.Errorf("parse command: %w", err)
	}

	if depsJSON.Valid {
		if err := json.Unmarshal([]byte(depsJSON.String), &j.Deps); err != nil {
			return nil, fmt.Errorf("parse deps: %w", err)
		}
	}

	return &j, nil
}

// nullStr returns a NULL NullString for empty strings.
func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// nullTime formats a time pointer as RFC3339Nano, or NULL if nil.
func nullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339Nano), Valid: true}
}

// marshalDeps encodes deps as JSON, returning NULL for empty slices.
func marshalDeps(deps []model.Dep) (sql.NullString, error) {
	if len(deps) == 0 {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(deps)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("db: marshal deps: %w", err)
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

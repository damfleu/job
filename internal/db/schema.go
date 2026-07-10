package db

import (
	"database/sql"
	"strings"
)

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    key        TEXT PRIMARY KEY,
    alias      TEXT,
    command    TEXT NOT NULL,
    work_dir   TEXT NOT NULL,
    log_file   TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending',
    reason     TEXT,
    exit_code  INTEGER,
    pid        INTEGER NOT NULL DEFAULT 0,
    pgid       INTEGER NOT NULL DEFAULT 0,
    hostname   TEXT,
    username   TEXT,
    created_at TEXT NOT NULL,
    started_at TEXT,
    stopped_at TEXT,
    deps       TEXT,
    context    TEXT,
    automated  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_jobs_status  ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_stopped ON jobs(stopped_at);

CREATE TABLE IF NOT EXISTS sequences (
    name       TEXT PRIMARY KEY,
    steps      TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sequence_job_refs (
    job_key       TEXT NOT NULL,
    sequence_name TEXT NOT NULL,
    PRIMARY KEY (job_key, sequence_name),
    FOREIGN KEY (sequence_name) REFERENCES sequences(name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_seq_refs_job ON sequence_job_refs(job_key);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// Idempotent migrations for existing databases.
	for _, stmt := range []string{
		`ALTER TABLE jobs ADD COLUMN context TEXT`,
		`ALTER TABLE jobs ADD COLUMN automated INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

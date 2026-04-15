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
    context    TEXT
);

CREATE INDEX IF NOT EXISTS idx_jobs_status  ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_stopped ON jobs(stopped_at);

CREATE TABLE IF NOT EXISTS metadata (
    key   TEXT PRIMARY KEY,
    value TEXT
);
`

func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// Idempotent: add context column to existing databases.
	_, err := db.Exec(`ALTER TABLE jobs ADD COLUMN context TEXT`)
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	return nil
}

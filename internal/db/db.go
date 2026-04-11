package db

import (
	"database/sql"
	"errors"

	"job/internal/model"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by Get when no job matches the key.
var ErrNotFound = errors.New("db: job not found")

// JobStore is the persistence interface for jobs.
type JobStore interface {
	Insert(job *model.Job) error
	Get(key string) (*model.Job, error)
	Update(job *model.Job) error
	Delete(key string) error
	ListActive() ([]*model.Job, error)
	ListCompleted(limit int) ([]*model.Job, error)
	Search(query string) ([]*model.Job, error)
	GetLastKey() (string, error)
	SetLastKey(key string) error
}

// DB wraps a SQLite database.
type DB struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path with WAL mode enabled.
func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := configure(sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}
	if err := migrate(sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}
	return &DB{db: sqldb}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func configure(db *sql.DB) error {
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return err
		}
	}
	return nil
}

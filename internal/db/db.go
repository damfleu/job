package db

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"job/internal/model"

	"modernc.org/sqlite"
)

func init() {
	// regexp(pattern, value) — used by the REGEXP operator: `col REGEXP ?`
	sqlite.MustRegisterDeterministicScalarFunction(
		"regexp",
		2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			pattern, ok := args[0].(string)
			if !ok {
				return nil, errors.New("regexp: pattern must be text")
			}
			s, ok := args[1].(string)
			if !ok {
				return nil, errors.New("regexp: value must be text")
			}
			matched, err := regexp.MatchString(pattern, s)
			if err != nil {
				return nil, fmt.Errorf("regexp: %w", err)
			}
			return matched, nil
		},
	)

	// cmd_str(command_json) — converts the JSON command array to a display string
	// so that REGEXP filters match against "make -j8" rather than `["make","-j8"]`.
	sqlite.MustRegisterDeterministicScalarFunction(
		"cmd_str",
		1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			s, _ := args[0].(string)
			var parts []string
			if err := json.Unmarshal([]byte(s), &parts); err != nil {
				return s, nil
			}
			return strings.Join(parts, " "), nil
		},
	)
}

// ErrNotFound is returned by Get when no job matches the key.
var ErrNotFound = errors.New("db: job not found")

// JobStore is the persistence interface for jobs.
type JobStore interface {
	Insert(job *model.Job) error
	Get(key string) (*model.Job, error)
	Update(job *model.Job) error
	Delete(key string) error
	ListActive(filter string) ([]*model.Job, error)
	ListCompleted(limit int, filter string) ([]*model.Job, error)
	ListCompletedBefore(t time.Time) ([]*model.Job, error)
	Search(query string) ([]*model.Job, error)
	FindByAlias(alias string) (*model.Job, error)
	FindByKeyPrefix(prefix string) ([]*model.Job, error)
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

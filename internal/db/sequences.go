package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"job/internal/model"
)

var ErrSequenceNotFound = errors.New("db: sequence not found")

func (d *DB) SaveSequence(seq *model.Sequence) error {
	steps, err := json.Marshal(seq.Steps)
	if err != nil {
		return fmt.Errorf("db: marshal sequence steps: %w", err)
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("db: save sequence: %w", err)
	}
	defer tx.Rollback()

	// Replace any existing sequence with the same name (cascades to refs).
	if _, err := tx.Exec(`DELETE FROM sequences WHERE name = ?`, seq.Name); err != nil {
		return fmt.Errorf("db: save sequence: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO sequences (name, steps, created_at) VALUES (?, ?, ?)`,
		seq.Name,
		string(steps),
		seq.CreatedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("db: save sequence %q: %w", seq.Name, err)
	}

	for _, key := range seq.Steps {
		if _, err := tx.Exec(
			`INSERT INTO sequence_job_refs (job_key, sequence_name) VALUES (?, ?)`,
			key, seq.Name,
		); err != nil {
			return fmt.Errorf("db: save sequence ref %s→%s: %w", key, seq.Name, err)
		}
	}

	return tx.Commit()
}

func (d *DB) GetSequence(name string) (*model.Sequence, error) {
	row := d.db.QueryRow(`SELECT name, steps, created_at FROM sequences WHERE name = ?`, name)
	seq, err := scanSequence(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSequenceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("db: get sequence %q: %w", name, err)
	}
	return seq, nil
}

func (d *DB) ListSequences() ([]*model.Sequence, error) {
	rows, err := d.db.Query(`SELECT name, steps, created_at FROM sequences ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("db: list sequences: %w", err)
	}
	defer rows.Close()

	var seqs []*model.Sequence
	for rows.Next() {
		seq, err := scanSequence(rows)
		if err != nil {
			return nil, err
		}
		seqs = append(seqs, seq)
	}
	return seqs, rows.Err()
}

func (d *DB) DeleteSequence(name string) error {
	res, err := d.db.Exec(`DELETE FROM sequences WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("db: delete sequence %q: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSequenceNotFound
	}
	return nil
}

func (d *DB) SequencesForJob(jobKey string) ([]string, error) {
	rows, err := d.db.Query(
		`SELECT sequence_name FROM sequence_job_refs WHERE job_key = ? ORDER BY sequence_name`,
		jobKey,
	)
	if err != nil {
		return nil, fmt.Errorf("db: sequences for job %s: %w", jobKey, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func scanSequence(s interface{ Scan(...any) error }) (*model.Sequence, error) {
	var (
		seq       model.Sequence
		stepsJSON string
		createdAt string
	)
	if err := s.Scan(&seq.Name, &stepsJSON, &createdAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(stepsJSON), &seq.Steps); err != nil {
		return nil, fmt.Errorf("parse sequence steps: %w", err)
	}
	var err error
	seq.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse sequence created_at: %w", err)
	}
	return &seq, nil
}

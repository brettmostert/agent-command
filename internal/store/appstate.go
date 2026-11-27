package store

import (
	"context"
	"database/sql"
	"errors"
)

// AppState returns a value the app stored ("" if there is none).
func (s *Store) AppState(ctx context.Context, key string) (string, error) {
	var value string
	err := s.q.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetAppState stores a value for the app.
func (s *Store) SetAppState(ctx context.Context, key, value string) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO app_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

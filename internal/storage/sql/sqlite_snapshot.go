package sql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CreateSQLiteSnapshot writes a transactionally consistent SQLite database
// using VACUUM INTO. The destination must not already exist.
func (s *SQLStore) CreateSQLiteSnapshot(ctx context.Context, destination string) error {
	if s == nil || !s.IsSQLite() {
		return errors.New("sqlite snapshot requires a SQLite store")
	}
	destination = filepath.Clean(destination)
	if destination == "." || destination == "" {
		return errors.New("snapshot destination is required")
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve snapshot destination: %w", err)
	}
	if _, err := os.Stat(abs); err == nil {
		return errors.New("snapshot destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect snapshot destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", abs); err != nil {
		_ = os.Remove(abs)
		return fmt.Errorf("create SQLite snapshot: %w", err)
	}
	return nil
}

package accounting

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
	_ "modernc.org/sqlite"
)

const (
	journalBusyTimeoutMS = 3_000
	maxPendingLimit      = 1_000
)

var errEventConflict = errors.New("journal event id already has a different payload")

// Journal stores token settlements already accepted by the process but not yet
// acknowledged by durable accounting.
type Journal struct {
	db   *sql.DB
	path string
}

// Open creates or opens a pending-settlement journal. Only a concrete file path
// is accepted so callers cannot accidentally select an in-memory database.
func Open(path string) (*Journal, error) {
	absolutePath, err := validateJournalPath(path)
	if err != nil {
		return nil, err
	}
	if err := prepareJournalParent(absolutePath); err != nil {
		return nil, fmt.Errorf("prepare journal parent: %w", err)
	}
	if err := prepareJournalFile(absolutePath); err != nil {
		return nil, fmt.Errorf("prepare journal file: %w", err)
	}

	db, err := sql.Open("sqlite", journalDSN(absolutePath))
	if err != nil {
		return nil, fmt.Errorf("open journal database: %w", err)
	}
	journal := &Journal{db: db, path: absolutePath}
	if err := journal.initialize(context.Background()); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close journal after initialization failure: %w", closeErr))
		}
		return nil, err
	}
	return journal, nil
}

// Append durably stores an event. Replaying the exact same payload is
// successful; reusing an id for different contents is rejected.
func (j *Journal) Append(ctx context.Context, event model.TokenSettlement) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("append token settlement: %w", err)
	}
	if err := validateSettlement(event); err != nil {
		return fmt.Errorf("append token settlement: %w", err)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("append token settlement %q: encode event: %w", event.ID, err)
	}

	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("append token settlement %q: begin transaction: %w", event.ID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO pending_events (id, occurred_at, event_json)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, event.ID, event.OccurredAt, payload)
	if err != nil {
		return fmt.Errorf("append token settlement %q: insert event: %w", event.ID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("append token settlement %q: read insert result: %w", event.ID, err)
	}

	if inserted == 0 {
		var storedPayload []byte
		err := tx.QueryRowContext(ctx, `
			SELECT event_json FROM pending_events WHERE id = ?
		`, event.ID).Scan(&storedPayload)
		if err != nil {
			return fmt.Errorf("append token settlement %q: read existing event: %w", event.ID, err)
		}
		if !bytes.Equal(storedPayload, payload) {
			return fmt.Errorf("append token settlement %q: %w", event.ID, errEventConflict)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("append token settlement %q: commit transaction: %w", event.ID, err)
	}
	committed = true
	return nil
}

// Pending returns the oldest unacknowledged events in occurrence order. Older
// events sort first, with the event id providing a stable tie-breaker.
func (j *Journal) Pending(ctx context.Context, limit int) ([]model.TokenSettlement, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read pending token settlements: %w", err)
	}
	if limit <= 0 {
		return nil, errors.New("read pending token settlements: limit must be positive")
	}
	if limit > maxPendingLimit {
		limit = maxPendingLimit
	}

	rows, err := j.db.QueryContext(ctx, `
		SELECT id, occurred_at, event_json
		FROM pending_events
		ORDER BY occurred_at ASC, id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("read pending token settlements: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]model.TokenSettlement, 0, limit)
	for rows.Next() {
		var (
			id         string
			occurredAt int64
			payload    []byte
		)
		if err := rows.Scan(&id, &occurredAt, &payload); err != nil {
			return nil, fmt.Errorf("read pending token settlements: scan event: %w", err)
		}

		var event model.TokenSettlement
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("read pending token settlements: decode event %q: %w", id, err)
		}
		if err := validateSettlement(event); err != nil {
			return nil, fmt.Errorf("read pending token settlements: validate event %q: %w", id, err)
		}
		if event.ID != id || event.OccurredAt != occurredAt {
			return nil, fmt.Errorf(
				"read pending token settlements: event %q identity does not match stored columns",
				id,
			)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read pending token settlements: iterate events: %w", err)
	}
	return events, nil
}

// Ack removes exactly the event id supplied by the caller. Acknowledging an
// already absent id is an idempotent success.
func (j *Journal) Ack(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("acknowledge token settlement: %w", err)
	}
	if id == "" || strings.TrimSpace(id) != id {
		return errors.New("acknowledge token settlement: id must be non-empty and trimmed")
	}

	if _, err := j.db.ExecContext(ctx, `
		DELETE FROM pending_events WHERE id = ?
	`, id); err != nil {
		return fmt.Errorf("acknowledge token settlement %q: %w", id, err)
	}
	return nil
}

// Count returns the number of unacknowledged events.
func (j *Journal) Count(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("count pending token settlements: %w", err)
	}

	var count int64
	if err := j.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pending_events
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending token settlements: %w", err)
	}
	return count, nil
}

// Check commits a write to a dedicated single-row health table. It verifies
// that the database and its disk are writable without touching pending events.
func (j *Journal) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("check journal writeability: %w", err)
	}

	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("check journal writeability: begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var nonce int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE journal_health
		SET nonce = nonce + 1
		WHERE singleton = 1
		RETURNING nonce
	`).Scan(&nonce); err != nil {
		return fmt.Errorf("check journal writeability: update health nonce: %w", err)
	}
	if nonce <= 0 {
		return errors.New("check journal writeability: health nonce did not advance")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("check journal writeability: commit probe: %w", err)
	}
	committed = true
	return nil
}

// Close releases the journal's single SQLite connection.
func (j *Journal) Close() error {
	if j == nil || j.db == nil {
		return nil
	}
	if err := j.db.Close(); err != nil {
		return fmt.Errorf("close journal: %w", err)
	}
	return nil
}

func (j *Journal) initialize(ctx context.Context) error {
	j.db.SetMaxOpenConns(1)
	j.db.SetMaxIdleConns(1)
	j.db.SetConnMaxLifetime(0)
	j.db.SetConnMaxIdleTime(0)

	var journalMode string
	if err := j.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		return fmt.Errorf("open journal database: read journal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("open journal database: journal mode=%q, want wal", journalMode)
	}

	var synchronous int
	if err := j.db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		return fmt.Errorf("open journal database: read synchronous mode: %w", err)
	}
	if synchronous != 2 {
		return fmt.Errorf("open journal database: synchronous mode=%d, want FULL (2)", synchronous)
	}

	var busyTimeout int
	if err := j.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		return fmt.Errorf("open journal database: read busy timeout: %w", err)
	}
	if busyTimeout <= 0 || busyTimeout > journalBusyTimeoutMS {
		return fmt.Errorf(
			"open journal database: busy timeout=%dms, want 1-%dms",
			busyTimeout,
			journalBusyTimeoutMS,
		)
	}

	if err := j.migrate(ctx); err != nil {
		return err
	}
	if err := tightenJournalPermissions(j.path); err != nil {
		return fmt.Errorf("open journal database: secure files: %w", err)
	}
	return nil
}

func (j *Journal) migrate(ctx context.Context) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate journal database: begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	statements := []string{
		`
			CREATE TABLE IF NOT EXISTS pending_events (
				id TEXT PRIMARY KEY NOT NULL,
				occurred_at INTEGER NOT NULL,
				event_json BLOB NOT NULL
			)
		`,
		`
			CREATE INDEX IF NOT EXISTS pending_events_occurred_at_id
			ON pending_events (occurred_at ASC, id ASC)
		`,
		`
			CREATE TABLE IF NOT EXISTS journal_health (
				singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
				nonce INTEGER NOT NULL CHECK (nonce >= 0)
			)
		`,
		`
			INSERT INTO journal_health (singleton, nonce)
			VALUES (1, 0)
			ON CONFLICT(singleton) DO NOTHING
		`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate journal database: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate journal database: commit: %w", err)
	}
	committed = true
	return nil
}

func validateJournalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("open journal database: path must not be empty")
	}
	if strings.EqualFold(path, ":memory:") || strings.HasPrefix(strings.ToLower(path), "file:") {
		return "", errors.New("open journal database: path must be a durable filesystem path, not a SQLite URI")
	}
	if strings.ContainsRune(path, '\x00') {
		return "", errors.New("open journal database: path contains a NUL byte")
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("open journal database: resolve path: %w", err)
	}
	return filepath.Clean(absolutePath), nil
}

func prepareJournalParent(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("parent %q is not a directory", parent)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	var missing []string
	for dir := parent; ; dir = filepath.Dir(dir) {
		info, statErr := os.Stat(dir)
		if statErr == nil {
			if !info.IsDir() {
				return fmt.Errorf("parent %q is not a directory", dir)
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		missing = append(missing, dir)

		next := filepath.Dir(dir)
		if next == dir {
			return fmt.Errorf("no existing ancestor for journal parent %q", parent)
		}
	}

	for i := len(missing) - 1; i >= 0; i-- {
		dir := missing[i]
		if err := os.Mkdir(dir, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return fmt.Errorf("create journal parent %q: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("set permissions on new journal parent %q: %w", dir, err)
		}
	}
	return nil
}

func prepareJournalFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		chmodErr := file.Chmod(0o600)
		closeErr := file.Close()
		if chmodErr != nil {
			_ = os.Remove(path)
			return fmt.Errorf("set permissions on new journal file %q: %w", path, chmodErr)
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return fmt.Errorf("close new journal file %q: %w", path, closeErr)
		}
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create journal file %q: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat journal file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("journal path %q is not a regular file", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set permissions on journal file %q: %w", path, err)
	}
	return nil
}

func tightenJournalPermissions(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Chmod(candidate, 0o600); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("set permissions on %q: %w", candidate, err)
		}
	}
	return nil
}

func journalDSN(path string) string {
	slashPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	databaseURL := url.URL{Scheme: "file", Path: slashPath}

	query := url.Values{}
	query.Add("_pragma", "busy_timeout(3000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", "wal_autocheckpoint(1000)")
	return databaseURL.String() + "?" + query.Encode()
}

func validateSettlement(event model.TokenSettlement) error {
	if event.ID == "" || strings.TrimSpace(event.ID) != event.ID {
		return errors.New("token settlement id must be non-empty and trimmed")
	}
	if len(event.ID) > 191 {
		return errors.New("token settlement id exceeds 191 bytes")
	}
	if event.TokenHash == "" || strings.TrimSpace(event.TokenHash) != event.TokenHash {
		return errors.New("token settlement token_hash must be non-empty and trimmed")
	}
	if len(event.TokenHash) > 100 {
		return errors.New("token settlement token_hash exceeds 100 bytes")
	}
	if event.OccurredAt <= 0 {
		return errors.New("token settlement occurred_at must be a positive Unix millisecond timestamp")
	}
	if !nonNegativeFinite(event.Duration) {
		return errors.New("token settlement duration must be finite and nonnegative")
	}
	if !nonNegativeFinite(event.FirstByteTime) {
		return errors.New("token settlement first_byte_time must be finite and nonnegative")
	}
	if event.PromptTokens < 0 {
		return errors.New("token settlement prompt_tokens must be nonnegative")
	}
	if event.CompletionTokens < 0 {
		return errors.New("token settlement completion_tokens must be nonnegative")
	}
	if event.CacheReadTokens < 0 {
		return errors.New("token settlement cache_read_tokens must be nonnegative")
	}
	if event.CacheCreationTokens < 0 {
		return errors.New("token settlement cache_creation_tokens must be nonnegative")
	}
	return model.ValidateTokenSettlementCosts(event.CostUSD, event.EffectiveCostUSD)
}

func nonNegativeFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

package accounting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

var _ interface {
	Append(context.Context, model.TokenSettlement) error
	Pending(context.Context, int) ([]model.TokenSettlement, error)
	Ack(context.Context, string) error
	Count(context.Context) (int64, error)
	Check(context.Context) error
	Close() error
} = (*Journal)(nil)

func openTestJournal(t *testing.T) (*Journal, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "billing-pending.db")
	journal, err := Open(path)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() {
		if err := journal.Close(); err != nil {
			t.Errorf("close journal: %v", err)
		}
	})
	return journal, path
}

func settlementEvent(id string, occurredAt int64) model.TokenSettlement {
	return model.TokenSettlement{
		ID:                  id,
		TokenHash:           model.HashToken("token-" + id),
		OccurredAt:          occurredAt,
		Duration:            12.5,
		IsStreaming:         true,
		FirstByteTime:       0.75,
		PromptTokens:        101,
		CompletionTokens:    202,
		CacheReadTokens:     303,
		CacheCreationTokens: 404,
		CostUSD:             0.125,
		EffectiveCostUSD:    0.25,
	}
}

func mustAppend(t *testing.T, ctx context.Context, journal *Journal, event model.TokenSettlement) {
	t.Helper()
	if err := journal.Append(ctx, event); err != nil {
		t.Fatalf("append event %q: %v", event.ID, err)
	}
}

func mustPending(t *testing.T, ctx context.Context, journal *Journal, limit int) []model.TokenSettlement {
	t.Helper()
	events, err := journal.Pending(ctx, limit)
	if err != nil {
		t.Fatalf("pending events: %v", err)
	}
	return events
}

func mustCount(t *testing.T, ctx context.Context, journal *Journal) int64 {
	t.Helper()
	count, err := journal.Count(ctx)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func TestJournalAppendPendingAckAndReopen(t *testing.T) {
	ctx := context.Background()
	journal, path := openTestJournal(t)

	middle := settlementEvent("event-b", 2_000)
	newest := settlementEvent("event-c", 3_000)
	oldest := settlementEvent("event-a", 1_000)
	sameTime := settlementEvent("event-a1", 2_000)

	mustAppend(t, ctx, journal, middle)
	mustAppend(t, ctx, journal, newest)
	mustAppend(t, ctx, journal, oldest)
	mustAppend(t, ctx, journal, sameTime)
	mustAppend(t, ctx, journal, middle)

	if got := mustCount(t, ctx, journal); got != 4 {
		t.Fatalf("count=%d after duplicate append, want 4", got)
	}

	firstPage := mustPending(t, ctx, journal, 2)
	wantFirstPage := []model.TokenSettlement{oldest, sameTime}
	if !reflect.DeepEqual(firstPage, wantFirstPage) {
		t.Fatalf("first pending page=%+v, want oldest events %+v", firstPage, wantFirstPage)
	}

	secondPage := mustPending(t, ctx, journal, 2)
	wantSecondPage := []model.TokenSettlement{oldest, sameTime}
	if !reflect.DeepEqual(secondPage, wantSecondPage) {
		t.Fatalf("second pending page=%+v, want deterministic order %+v", secondPage, wantSecondPage)
	}
	if !reflect.DeepEqual(firstPage, secondPage) {
		t.Fatal("pending order changed between identical queries")
	}

	if err := journal.Close(); err != nil {
		t.Fatalf("close before reopen: %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen journal: %v", err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened journal: %v", err)
		}
	}()

	if got := mustCount(t, ctx, reopened); got != 4 {
		t.Fatalf("count=%d after reopen, want 4", got)
	}
	if got := mustPending(t, ctx, reopened, 10); !reflect.DeepEqual(got, []model.TokenSettlement{oldest, sameTime, middle, newest}) {
		t.Fatalf("pending after reopen=%+v, want all events in occurrence order", got)
	}

	if err := reopened.Ack(ctx, middle.ID); err != nil {
		t.Fatalf("ack event: %v", err)
	}
	if err := reopened.Ack(ctx, middle.ID); err != nil {
		t.Fatalf("repeat ack must be idempotent: %v", err)
	}
	if got := mustCount(t, ctx, reopened); got != 3 {
		t.Fatalf("count=%d after ack, want 3", got)
	}
	if got := mustPending(t, ctx, reopened, 10); !reflect.DeepEqual(got, []model.TokenSettlement{oldest, sameTime, newest}) {
		t.Fatalf("pending after ack=%+v, want acked event removed", got)
	}
}

func TestJournalAppendRejectsConflictingPayloadWithoutOverwrite(t *testing.T) {
	ctx := context.Background()
	journal, _ := openTestJournal(t)

	original := settlementEvent("event-conflict", 1_000)
	mustAppend(t, ctx, journal, original)

	conflict := original
	conflict.PromptTokens++
	if err := journal.Append(ctx, conflict); err == nil {
		t.Fatal("append with same id and different payload succeeded")
	}

	if got := mustCount(t, ctx, journal); got != 1 {
		t.Fatalf("count=%d after conflicting append, want 1", got)
	}
	if got := mustPending(t, ctx, journal, 10); !reflect.DeepEqual(got, []model.TokenSettlement{original}) {
		t.Fatalf("pending=%+v after conflicting append, want original payload", got)
	}
}

func TestJournalRejectsCanceledContext(t *testing.T) {
	journal, _ := openTestJournal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	event := settlementEvent("event-canceled", 1_000)
	if err := journal.Append(ctx, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("append canceled context error=%v, want context.Canceled", err)
	}
	if _, err := journal.Pending(ctx, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("pending canceled context error=%v, want context.Canceled", err)
	}
	if err := journal.Ack(ctx, event.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("ack canceled context error=%v, want context.Canceled", err)
	}
	if _, err := journal.Count(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("count canceled context error=%v, want context.Canceled", err)
	}
	if err := journal.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("check canceled context error=%v, want context.Canceled", err)
	}

	if got := mustCount(t, context.Background(), journal); got != 0 {
		t.Fatalf("count=%d after canceled operations, want 0", got)
	}
}

func TestJournalPendingPreservesMalformedRows(t *testing.T) {
	ctx := context.Background()
	journal, _ := openTestJournal(t)

	if _, err := journal.db.ExecContext(ctx, `
		INSERT INTO pending_events (id, occurred_at, event_json)
		VALUES (?, ?, ?)
	`, "bad-json", 500, []byte(`{"id":"bad-json"`)); err != nil {
		t.Fatalf("insert malformed row: %v", err)
	}
	mustAppend(t, ctx, journal, settlementEvent("valid-event", 1_000))

	events, err := journal.Pending(ctx, 10)
	if err == nil {
		t.Fatalf("pending returned events %+v for malformed row, want error", events)
	}
	if !strings.Contains(err.Error(), "bad-json") {
		t.Fatalf("pending malformed row error=%v, want event id context", err)
	}
	if got := mustCount(t, ctx, journal); got != 2 {
		t.Fatalf("count=%d after malformed read, want 2", got)
	}

	var payload string
	if err := journal.db.QueryRowContext(ctx, `
		SELECT event_json FROM pending_events WHERE id = ?
	`, "bad-json").Scan(&payload); err != nil {
		t.Fatalf("malformed row was deleted after read failure: %v", err)
	}
}

func TestJournalPendingRejectsMismatchedStoredIdentity(t *testing.T) {
	ctx := context.Background()
	journal, _ := openTestJournal(t)

	payload, err := json.Marshal(settlementEvent("payload-id", 2_000))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := journal.db.ExecContext(ctx, `
		INSERT INTO pending_events (id, occurred_at, event_json)
		VALUES (?, ?, ?)
	`, "column-id", 1_000, payload); err != nil {
		t.Fatalf("insert mismatched row: %v", err)
	}

	if _, err := journal.Pending(ctx, 10); err == nil {
		t.Fatal("pending accepted row whose payload identity differs from its columns")
	}
	if got := mustCount(t, ctx, journal); got != 1 {
		t.Fatalf("count=%d after identity mismatch, want 1", got)
	}
}

func TestJournalRejectsEventsUnsafeForPrimaryAccounting(t *testing.T) {
	ctx := context.Background()
	journal, _ := openTestJournal(t)

	cases := []struct {
		name  string
		event model.TokenSettlement
	}{
		{
			name: "oversized id",
			event: func() model.TokenSettlement {
				event := settlementEvent("event-oversized-id", 1_000)
				event.ID = strings.Repeat("x", 192)
				return event
			}(),
		},
		{
			name: "oversized token hash",
			event: func() model.TokenSettlement {
				event := settlementEvent("event-oversized-hash", 1_000)
				event.TokenHash = strings.Repeat("a", 101)
				return event
			}(),
		},
		{
			name: "microUSD int64 boundary",
			event: func() model.TokenSettlement {
				event := settlementEvent("event-micro-boundary", 1_000)
				event.EffectiveCostUSD = float64(math.MaxInt64) / 1_000_000
				return event
			}(),
		},
		{
			name: "microUSD overflow",
			event: func() model.TokenSettlement {
				event := settlementEvent("event-micro-overflow", 1_000)
				event.EffectiveCostUSD = math.MaxFloat64
				return event
			}(),
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := journal.Append(ctx, tt.event); err == nil {
				t.Fatalf("append accepted event that primary accounting would reject: %+v", tt.event)
			}
			if got := mustCount(t, ctx, journal); got != 0 {
				t.Fatalf("count=%d after rejected event, want 0", got)
			}
		})
	}

	safe := settlementEvent("event-safe-large", 1_000)
	safe.EffectiveCostUSD = 9_000_000_000_000
	mustAppend(t, ctx, journal, safe)
	if got := mustCount(t, ctx, journal); got != 1 {
		t.Fatalf("count=%d after safe large value, want 1", got)
	}
}

func TestJournalCheckWritesHealthProbeWithoutMutatingEvents(t *testing.T) {
	ctx := context.Background()
	journal, _ := openTestJournal(t)

	for i := int64(1); i <= 3; i++ {
		if err := journal.Check(ctx); err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if got := healthNonce(t, ctx, journal); got != i {
			t.Fatalf("health nonce=%d after check %d, want %d", got, i, i)
		}
	}

	event := settlementEvent("event-check", 1_000)
	mustAppend(t, ctx, journal, event)
	if err := journal.Check(ctx); err != nil {
		t.Fatalf("check with pending event: %v", err)
	}
	if got := healthNonce(t, ctx, journal); got != 4 {
		t.Fatalf("health nonce=%d after fourth check, want 4", got)
	}
	if got := healthRowCount(t, ctx, journal); got != 1 {
		t.Fatalf("health rows=%d after repeated checks, want 1", got)
	}
	if got := mustPending(t, ctx, journal, 10); !reflect.DeepEqual(got, []model.TokenSettlement{event}) {
		t.Fatalf("pending=%+v after checks, want event unchanged", got)
	}

	beforeFailure := healthNonce(t, ctx, journal)
	if _, err := journal.db.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		t.Fatalf("enable query-only mode: %v", err)
	}
	if err := journal.Check(ctx); err == nil {
		t.Fatal("check succeeded while database rejected writes")
	}
	if _, err := journal.db.ExecContext(ctx, `PRAGMA query_only = OFF`); err != nil {
		t.Fatalf("disable query-only mode: %v", err)
	}
	if got := healthNonce(t, ctx, journal); got != beforeFailure {
		t.Fatalf("health nonce=%d after failed probe, want rollback to %d", got, beforeFailure)
	}
	if got := mustCount(t, ctx, journal); got != 1 {
		t.Fatalf("count=%d after failed probe, want 1", got)
	}

	if err := journal.Check(ctx); err != nil {
		t.Fatalf("check after restoring writeability: %v", err)
	}
	if got := healthNonce(t, ctx, journal); got != beforeFailure+1 {
		t.Fatalf("health nonce=%d after recovered check, want %d", got, beforeFailure+1)
	}
}

func TestJournalStoresOnlyTokenHash(t *testing.T) {
	ctx := context.Background()
	journal, path := openTestJournal(t)

	secret := "sk-live-credential-that-must-not-be-persisted"
	event := settlementEvent("event-credential", 1_000)
	event.TokenHash = model.HashToken(secret)
	mustAppend(t, ctx, journal, event)

	var payload []byte
	if err := journal.db.QueryRowContext(ctx, `
		SELECT event_json FROM pending_events WHERE id = ?
	`, event.ID).Scan(&payload); err != nil {
		t.Fatalf("read stored payload: %v", err)
	}
	if !bytes.Contains(payload, []byte(`"token_hash":"`+event.TokenHash+`"`)) {
		t.Fatalf("stored payload does not contain token hash: %s", payload)
	}
	if bytes.Contains(payload, []byte(`"token":`)) {
		t.Fatalf("stored payload contains a plaintext token field: %s", payload)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("close journal: %v", err)
	}

	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read journal file %s: %v", candidate, err)
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("journal file %s contains plaintext credential", candidate)
		}
	}
}

func TestJournalRejectsNonDurablePaths(t *testing.T) {
	for _, path := range []string{
		":memory:",
		"file:journal.db",
		"file::memory:",
		"file:///tmp/journal.db",
	} {
		t.Run(path, func(t *testing.T) {
			journal, err := Open(path)
			if err == nil {
				_ = journal.Close()
				t.Fatalf("Open(%q) succeeded, want non-durable path rejection", path)
			}
			if journal != nil {
				t.Fatalf("Open(%q) returned journal with error", path)
			}
		})
	}
}

func TestJournalSetsPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve POSIX permission bits")
	}

	parent := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(parent, "billing-pending.db")
	journal, err := Open(path)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer func() {
		if err := journal.Close(); err != nil {
			t.Errorf("close journal: %v", err)
		}
	}()

	parentInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if got := parentInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("parent permissions=%o, want 700", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal permissions=%o, want 600", got)
	}
}

func TestJournalPreservesExistingParentPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve POSIX permission bits")
	}

	parent := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(parent, 0o751); err != nil {
		t.Fatalf("create existing parent: %v", err)
	}
	if err := os.Chmod(parent, 0o751); err != nil {
		t.Fatalf("set existing parent permissions: %v", err)
	}

	journal, err := Open(filepath.Join(parent, "billing-pending.db"))
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer func() {
		if err := journal.Close(); err != nil {
			t.Errorf("close journal: %v", err)
		}
	}()

	parentInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat existing parent: %v", err)
	}
	if got := parentInfo.Mode().Perm(); got != 0o751 {
		t.Fatalf("existing parent permissions=%o, want preserved 751", got)
	}
	fileInfo, err := os.Stat(filepath.Join(parent, "billing-pending.db"))
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal permissions=%o, want 600", got)
	}
}

func healthNonce(t *testing.T, ctx context.Context, journal *Journal) int64 {
	t.Helper()
	var nonce int64
	if err := journal.db.QueryRowContext(ctx, `
		SELECT nonce FROM journal_health WHERE singleton = 1
	`).Scan(&nonce); err != nil {
		t.Fatalf("read health nonce: %v", err)
	}
	return nonce
}

func healthRowCount(t *testing.T, ctx context.Context, journal *Journal) int64 {
	t.Helper()
	var count int64
	if err := journal.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM journal_health
	`).Scan(&count); err != nil {
		t.Fatalf("count health rows: %v", err)
	}
	return count
}

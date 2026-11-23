//go:build sonic

package storage

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage/schema"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"

	_ "modernc.org/sqlite"
)

type authAccountingUpgradeSnapshot struct {
	SuccessCount             int64
	FailureCount             int64
	StreamCount              int64
	NonStreamCount           int64
	PromptTokensTotal        int64
	CompletionTokensTotal    int64
	CacheReadTokensTotal     int64
	CacheCreationTokensTotal int64
	CostUsedMicroUSD         int64
	CostLimitMicroUSD        int64
	LastUsedAt               int64
	StreamAvgTTFB            float64
	NonStreamAvgRT           float64
	TotalCostUSD             float64
	EffectiveCostUSD         float64
}

type settlementUpgradeSnapshot struct {
	ID                  string
	TokenHash           string
	OccurredAt          int64
	Duration            float64
	IsStreaming         int
	FirstByteTime       float64
	PromptTokens        int64
	CompletionTokens    int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	CostUSD             float64
	EffectiveCostUSD    float64
	CostInvalid         int
}

func loadAuthAccountingUpgradeSnapshot(ctx context.Context, store *sqlstore.SQLStore, tokenHash string) (authAccountingUpgradeSnapshot, error) {
	var snapshot authAccountingUpgradeSnapshot
	err := store.QueryRowContext(ctx, `
		SELECT
			success_count, failure_count, stream_count, non_stream_count,
			prompt_tokens_total, completion_tokens_total,
			cache_read_tokens_total, cache_creation_tokens_total,
			cost_used_microusd, cost_limit_microusd, last_used_at,
			stream_avg_ttfb, non_stream_avg_rt, total_cost_usd, effective_cost_usd
		FROM auth_tokens
		WHERE token = ?
	`, tokenHash).Scan(
		&snapshot.SuccessCount,
		&snapshot.FailureCount,
		&snapshot.StreamCount,
		&snapshot.NonStreamCount,
		&snapshot.PromptTokensTotal,
		&snapshot.CompletionTokensTotal,
		&snapshot.CacheReadTokensTotal,
		&snapshot.CacheCreationTokensTotal,
		&snapshot.CostUsedMicroUSD,
		&snapshot.CostLimitMicroUSD,
		&snapshot.LastUsedAt,
		&snapshot.StreamAvgTTFB,
		&snapshot.NonStreamAvgRT,
		&snapshot.TotalCostUSD,
		&snapshot.EffectiveCostUSD,
	)
	return snapshot, err
}

func loadSettlementUpgradeSnapshots(ctx context.Context, store *sqlstore.SQLStore, tokenHash string) ([]settlementUpgradeSnapshot, error) {
	rows, err := store.QueryContext(ctx, `
		SELECT
			id, token_hash, occurred_at, duration, is_streaming, first_byte_time,
			prompt_tokens, completion_tokens, cache_read_tokens, cache_creation_tokens,
			cost_usd, effective_cost_usd, cost_invalid
		FROM token_settlements
		WHERE token_hash = ?
		ORDER BY id
	`, tokenHash)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var snapshots []settlementUpgradeSnapshot
	for rows.Next() {
		var snapshot settlementUpgradeSnapshot
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.TokenHash,
			&snapshot.OccurredAt,
			&snapshot.Duration,
			&snapshot.IsStreaming,
			&snapshot.FirstByteTime,
			&snapshot.PromptTokens,
			&snapshot.CompletionTokens,
			&snapshot.CacheReadTokens,
			&snapshot.CacheCreationTokens,
			&snapshot.CostUSD,
			&snapshot.EffectiveCostUSD,
			&snapshot.CostInvalid,
		); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func logColumnSet(ctx context.Context, store *sqlstore.SQLStore) (map[string]bool, error) {
	rows, err := store.QueryContext(ctx, "PRAGMA table_info(logs)")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue any
			primaryKey   int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func TestMigrateSQLiteLogsCostSemanticsAddsColumnsWithoutBackfill(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schema.DefineLogsTable().BuildSQLite()); err != nil {
		t.Fatalf("create pre-cost-semantics logs table: %v", err)
	}
	for _, column := range []string{"cost_detail_json", "cost_basis", "cost_status", "cost_metadata_version"} {
		if _, err := db.ExecContext(ctx, "ALTER TABLE logs DROP COLUMN "+column); err != nil {
			t.Fatalf("drop %s from legacy logs: %v", column, err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO logs(time, model, log_source, channel_id, status_code, message, cost, cost_source)
		VALUES(60000, 'legacy-cost-model', 'proxy', 7, 200, 'legacy-cost-row', 0.37, 'site_pricing');
	`); err != nil {
		t.Fatalf("create legacy logs: %v", err)
	}

	if err := migrate(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var version int
	var status, basis, detail string
	var cost float64
	if err := db.QueryRowContext(ctx, `
		SELECT cost_metadata_version, cost_status, cost_basis, cost_detail_json, cost
		FROM logs WHERE message='legacy-cost-row'
	`).Scan(&version, &status, &basis, &detail, &cost); err != nil {
		t.Fatalf("read migrated legacy row: %v", err)
	}
	if version != 0 || status != "" || basis != "" || detail != "" {
		t.Fatalf("legacy metadata=(%d,%q,%q,%q), want zero defaults", version, status, basis, detail)
	}
	if cost != 0.37 {
		t.Fatalf("legacy cost=%v, want 0.37 unchanged", cost)
	}
}

func TestMigrateSQLiteCostSemanticsPreservesTokenAccountingAndSettlementReceipts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cost-upgrade-accounting.db")
	ctx := context.Background()
	first, err := createSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create current-schema store: %v", err)
	}
	defer func() { _ = first.Close() }()

	const effectiveCostMarker = "auth_tokens_effective_cost_backfill_site_pricing_v3"
	tokenHash := model.HashToken("cost-semantics-upgrade-accounting")
	if err := first.CreateAuthToken(ctx, &model.AuthToken{
		ID:                701,
		Token:             tokenHash,
		Description:       "cost semantics upgrade accounting",
		CreatedAt:         time.Now().Add(-time.Hour),
		IsActive:          true,
		FailureCount:      4,
		CostLimitMicroUSD: 5_000_000,
		MaxConcurrency:    1,
	}); err != nil {
		t.Fatalf("create auth token: %v", err)
	}
	if _, err := first.ExecContext(ctx, "UPDATE auth_tokens SET failure_count = 4 WHERE token = ?", tokenHash); err != nil {
		t.Fatalf("seed auth token failure count: %v", err)
	}

	occurredAt := time.Now().Add(-time.Minute).UnixMilli()
	events := []model.TokenSettlement{
		{
			ID:                  "cost-upgrade-settlement-nonstream",
			TokenHash:           tokenHash,
			OccurredAt:          occurredAt,
			Duration:            2.5,
			PromptTokens:        11,
			CompletionTokens:    7,
			CacheReadTokens:     3,
			CacheCreationTokens: 2,
			CostUSD:             0.4,
			EffectiveCostUSD:    0.2,
		},
		{
			ID:                  "cost-upgrade-settlement-stream-invalid-cost",
			TokenHash:           tokenHash,
			OccurredAt:          occurredAt + 1_000,
			Duration:            0.75,
			IsStreaming:         true,
			FirstByteTime:       0.15,
			PromptTokens:        5,
			CompletionTokens:    3,
			CacheReadTokens:     1,
			CacheCreationTokens: 1,
			CostInvalid:         true,
		},
	}
	for _, event := range events {
		if err := first.ApplyTokenSettlement(ctx, event); err != nil {
			t.Fatalf("apply settlement %s: %v", event.ID, err)
		}
	}

	token, err := first.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("load auth token: %v", err)
	}
	// A later log would rewrite effective_cost_usd and cost_used_microusd if
	// the existing one-time backfill marker were lost during this upgrade.
	if _, err := first.ExecContext(ctx, `
		INSERT INTO logs(time, model, log_source, channel_id, status_code, message, auth_token_id, cost, cost_multiplier, cost_source)
		VALUES(?, 'gpt-cost', 'proxy', 0, 200, 'divergent-backfill-log', ?, 99, 3000, 'site_pricing')
	`, time.Now().UnixMilli(), token.ID); err != nil {
		t.Fatalf("insert divergent backfill log: %v", err)
	}

	var markerCount int
	if err := first.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", effectiveCostMarker).Scan(&markerCount); err != nil {
		t.Fatalf("read effective-cost marker: %v", err)
	}
	if markerCount != 1 {
		t.Fatalf("effective-cost marker count=%d, want 1", markerCount)
	}

	beforeAccounting, err := loadAuthAccountingUpgradeSnapshot(ctx, first, tokenHash)
	if err != nil {
		t.Fatalf("load accounting before upgrade: %v", err)
	}
	if beforeAccounting.SuccessCount != 2 ||
		beforeAccounting.FailureCount != 4 ||
		beforeAccounting.PromptTokensTotal != 16 ||
		beforeAccounting.CompletionTokensTotal != 10 ||
		beforeAccounting.CacheReadTokensTotal != 4 ||
		beforeAccounting.CacheCreationTokensTotal != 3 {
		t.Fatalf("unexpected accounting fixture: %+v", beforeAccounting)
	}
	if math.Abs(beforeAccounting.TotalCostUSD-0.4) > 1e-9 ||
		math.Abs(beforeAccounting.EffectiveCostUSD-0.2) > 1e-9 ||
		beforeAccounting.CostUsedMicroUSD != 200_000 {
		t.Fatalf("unexpected cost fixture: %+v", beforeAccounting)
	}
	beforeReceipts, err := loadSettlementUpgradeSnapshots(ctx, first, tokenHash)
	if err != nil {
		t.Fatalf("load receipts before upgrade: %v", err)
	}
	if len(beforeReceipts) != len(events) {
		t.Fatalf("receipt count=%d, want %d", len(beforeReceipts), len(events))
	}

	columns, err := logColumnSet(ctx, first)
	if err != nil {
		t.Fatalf("read logs columns before downgrade simulation: %v", err)
	}
	costColumns := []string{"cost_detail_json", "cost_basis", "cost_status", "cost_metadata_version"}
	for _, column := range costColumns {
		if !columns[column] {
			t.Fatalf("logs missing current column %s before downgrade simulation", column)
		}
		if _, err := first.ExecContext(ctx, "ALTER TABLE logs DROP COLUMN "+column); err != nil {
			t.Fatalf("drop pre-cost-semantics column %s: %v", column, err)
		}
	}
	columns, err = logColumnSet(ctx, first)
	if err != nil {
		t.Fatalf("read logs columns after downgrade simulation: %v", err)
	}
	for _, column := range costColumns {
		if columns[column] {
			t.Fatalf("logs still has %s after downgrade simulation", column)
		}
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close pre-cost-semantics store: %v", err)
	}

	reopened, err := createSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen and migrate cost-semantics store: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	columns, err = logColumnSet(ctx, reopened)
	if err != nil {
		t.Fatalf("read logs columns after reopen: %v", err)
	}
	for _, column := range costColumns {
		if !columns[column] {
			t.Fatalf("reopened logs missing %s", column)
		}
	}
	if err := reopened.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", effectiveCostMarker).Scan(&markerCount); err != nil {
		t.Fatalf("read effective-cost marker after reopen: %v", err)
	}
	if markerCount != 1 {
		t.Fatalf("effective-cost marker count=%d after reopen, want 1", markerCount)
	}

	afterAccounting, err := loadAuthAccountingUpgradeSnapshot(ctx, reopened, tokenHash)
	if err != nil {
		t.Fatalf("load accounting after upgrade: %v", err)
	}
	if afterAccounting != beforeAccounting {
		t.Fatalf("auth accounting changed across upgrade:\nbefore=%+v\nafter=%+v", beforeAccounting, afterAccounting)
	}
	afterReceipts, err := loadSettlementUpgradeSnapshots(ctx, reopened, tokenHash)
	if err != nil {
		t.Fatalf("load receipts after upgrade: %v", err)
	}
	if len(afterReceipts) != len(beforeReceipts) {
		t.Fatalf("receipt count changed across upgrade: before=%d after=%d", len(beforeReceipts), len(afterReceipts))
	}
	for i := range beforeReceipts {
		if afterReceipts[i] != beforeReceipts[i] {
			t.Fatalf("receipt %d changed across upgrade:\nbefore=%+v\nafter=%+v", i, beforeReceipts[i], afterReceipts[i])
		}
	}
}

func TestCostMetadataSurvivesSQLiteReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cost-reopen.db")
	ctx := context.Background()
	store, err := CreateSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	now := time.Now()
	entry := &model.LogEntry{
		Time:                model.JSONTime{Time: now},
		Model:               "gpt-cost",
		LogSource:           model.LogSourceProxy,
		ChannelID:           3,
		StatusCode:          200,
		Message:             "reopen",
		RequestID:           "cost-reopen",
		CostMetadataVersion: 1,
		CostStatus:          "partial_estimate",
		CostBasis:           "mixed",
		CostDetailJSON:      `{"model_cost_usd":0,"tool_cost_usd":0.2,"currency":"USD"}`,
		Cost:                0.2,
		CostMultiplier:      1,
		CostSource:          "site_pricing",
	}
	if err := store.AddLog(ctx, entry); err != nil {
		t.Fatalf("AddLog: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := CreateSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	logs, err := reopened.ListLogsRange(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10, 0, nil)
	if err != nil {
		t.Fatalf("ListLogsRange after reopen: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs len=%d, want 1", len(logs))
	}
	got := logs[0]
	if got.CostMetadataVersion != entry.CostMetadataVersion ||
		got.CostStatus != entry.CostStatus ||
		got.CostBasis != entry.CostBasis ||
		got.CostDetailJSON != entry.CostDetailJSON ||
		got.Cost != entry.Cost {
		t.Fatalf("reopened metadata=%+v, want version/status/basis/detail/cost from %+v", got, entry)
	}
}

func TestSyncManagerRestoreLogsSnapshotCopiesCostMetadata(t *testing.T) {
	source := createTestStoreForSync(t, "cost_copy_source")
	target := createTestStoreForSync(t, "cost_copy_target")
	defer func() {
		_ = source.Close()
		_ = target.Close()
	}()
	ctx := context.Background()
	now := time.Now()
	entry := &model.LogEntry{
		Time:                model.JSONTime{Time: now},
		Model:               "gpt-cost",
		LogSource:           model.LogSourceProxy,
		ChannelID:           4,
		StatusCode:          200,
		Message:             "snapshot-copy",
		RequestID:           "cost-snapshot-copy",
		CostMetadataVersion: 1,
		CostStatus:          "estimated",
		CostBasis:           "token",
		CostDetailJSON:      `{"model_cost_usd":0.25,"tool_cost_usd":0,"currency":"USD"}`,
		Cost:                0.25,
		CostMultiplier:      1,
		CostSource:          "local_estimate",
	}
	if err := source.AddLog(ctx, entry); err != nil {
		t.Fatalf("source AddLog: %v", err)
	}
	if err := NewSyncManager(source, target).restoreLogsSnapshot(ctx, -1); err != nil {
		t.Fatalf("restoreLogsSnapshot: %v", err)
	}

	logs, err := target.ListLogsRange(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10, 0, nil)
	if err != nil {
		t.Fatalf("target ListLogsRange: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("target logs len=%d, want 1", len(logs))
	}
	got := logs[0]
	if got.CostMetadataVersion != entry.CostMetadataVersion ||
		got.CostStatus != entry.CostStatus ||
		got.CostBasis != entry.CostBasis ||
		got.CostDetailJSON != entry.CostDetailJSON ||
		got.Cost != entry.Cost ||
		got.CostSource != entry.CostSource {
		t.Fatalf("copied metadata=%+v, want %+v", got, entry)
	}
}

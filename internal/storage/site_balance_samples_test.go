package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage/schema"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func balanceSampleFixture(t *testing.T, id int64, day string, hour int) *model.SiteAccountDailyBalance {
	t.Helper()
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	return &model.SiteAccountDailyBalance{SiteAccountID: id, LocalDay: day, ScheduledAt: at.Add(time.Duration(hour) * time.Hour).UnixMilli(), Status: "unknown", Currency: "USD"}
}

func TestBalanceSamplesClaimAndTerminalAreImmutablePerSlot(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	first := balanceSampleFixture(t, id, "2026-11-28", 9)
	if claimed, err := store.CreateSiteAccountBalanceSample(ctx, first); err != nil || !claimed {
		t.Fatalf("first claim=%v err=%v", claimed, err)
	}
	duplicate := *first
	duplicate.LocalDay, duplicate.Currency = "2026-11-29", "CNY"
	if claimed, err := store.CreateSiteAccountBalanceSample(ctx, &duplicate); err != nil || claimed {
		t.Fatalf("same slot with different metadata claim=%v err=%v", claimed, err)
	}
	second := balanceSampleFixture(t, id, first.LocalDay, 10)
	if claimed, err := store.CreateSiteAccountBalanceSample(ctx, second); err != nil || !claimed {
		t.Fatalf("next same-day slot claim=%v err=%v", claimed, err)
	}
	amount := -1.25
	first.Status, first.Balance, first.Currency, first.CollectedAt = "success", &amount, " usd ", first.ScheduledAt+1000
	if finished, err := store.CompleteSiteAccountBalanceSample(ctx, first); err != nil || !finished {
		t.Fatalf("first completion=%v err=%v", finished, err)
	}
	changed := 99.0
	first.Balance = &changed
	if finished, err := store.CompleteSiteAccountBalanceSample(ctx, first); err != nil || finished {
		t.Fatalf("repeat completion=%v err=%v", finished, err)
	}
	wrongDay := *second
	wrongDay.LocalDay, wrongDay.Status, wrongDay.Balance, wrongDay.CollectedAt = "2026-11-29", "success", &changed, second.ScheduledAt
	if finished, err := store.CompleteSiteAccountBalanceSample(ctx, &wrongDay); err != nil || finished {
		t.Fatalf("completion with wrong claim metadata=%v err=%v", finished, err)
	}
	rows, err := store.ListSiteAccountBalanceSamples(ctx, first.LocalDay, first.LocalDay)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].ScheduledAt != first.ScheduledAt || rows[0].Balance == nil || *rows[0].Balance != amount || rows[0].Currency != "USD" || rows[0].CreatedAt <= 0 {
		t.Fatalf("first observation changed: %+v", rows[0])
	}
	if rows[1].ScheduledAt != second.ScheduledAt || rows[1].Status != "unknown" || rows[1].Balance != nil {
		t.Fatalf("next slot changed: %+v", rows[1])
	}
	if daily, err := store.ListSiteAccountDailyBalances(ctx, "", ""); err != nil || len(daily) != 0 {
		t.Fatalf("interval observations leaked into daily history: %+v err=%v", daily, err)
	}
}

func TestBalanceSamplesFailuresRemainNullAndTerminal(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	for hour := 9; hour <= 10; hour++ {
		sample := balanceSampleFixture(t, id, "2026-11-28", hour)
		if claimed, err := store.CreateSiteAccountBalanceSample(ctx, sample); err != nil || !claimed {
			t.Fatalf("claim=%v err=%v", claimed, err)
		}
		if hour == 10 {
			sample.Status, sample.ErrorCode, sample.CollectedAt = "failed", "provider_timeout", sample.ScheduledAt+1
			if finished, err := store.CompleteSiteAccountBalanceSample(ctx, sample); err != nil || !finished {
				t.Fatalf("failed completion=%v err=%v", finished, err)
			}
			zero := 0.0
			sample.Status, sample.Balance = "success", &zero
			if finished, err := store.CompleteSiteAccountBalanceSample(ctx, sample); err != nil || finished {
				t.Fatalf("failed slot was rewritten: finished=%v err=%v", finished, err)
			}
		}
	}
	rows, err := store.ListSiteAccountBalanceSamples(ctx, "", "")
	if err != nil || len(rows) != 2 || rows[0].Status != "unknown" || rows[1].Status != "failed" || rows[1].ErrorCode != "provider_timeout" || rows[0].Balance != nil || rows[1].Balance != nil {
		t.Fatalf("unknown/failure became zero: rows=%+v err=%v", rows, err)
	}
}

func TestBalanceSamplesValidateWithoutChangingTheClaim(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	base := balanceSampleFixture(t, id, "2026-11-28", 9)
	amount := 1.0
	for _, change := range []func(*model.SiteAccountDailyBalance){
		func(v *model.SiteAccountDailyBalance) { v.SiteAccountID = 0 },
		func(v *model.SiteAccountDailyBalance) { v.ScheduledAt = 0 },
		func(v *model.SiteAccountDailyBalance) { v.LocalDay = "2026-02-30" },
		func(v *model.SiteAccountDailyBalance) { v.LocalDay = "2026-1-01" },
		func(v *model.SiteAccountDailyBalance) { v.Status = "success" },
		func(v *model.SiteAccountDailyBalance) { v.Balance = &amount },
		func(v *model.SiteAccountDailyBalance) { v.Currency = strings.Repeat("X", 17) },
		func(v *model.SiteAccountDailyBalance) { v.CollectedAt = v.ScheduledAt },
		func(v *model.SiteAccountDailyBalance) { v.ErrorCode = "provider_timeout" },
	} {
		invalid := *base
		change(&invalid)
		if claimed, err := store.CreateSiteAccountBalanceSample(ctx, &invalid); err == nil || claimed {
			t.Fatalf("accepted invalid claim %+v: claimed=%v err=%v", invalid, claimed, err)
		}
	}
	if claimed, err := store.CreateSiteAccountBalanceSample(ctx, nil); err == nil || claimed {
		t.Fatalf("nil claim=%v err=%v", claimed, err)
	}
	if claimed, err := store.CreateSiteAccountBalanceSample(ctx, base); err != nil || !claimed {
		t.Fatalf("valid claim=%v err=%v", claimed, err)
	}
	base.Status, base.Balance, base.CollectedAt = "success", &amount, base.ScheduledAt
	for _, change := range []func(*model.SiteAccountDailyBalance){
		func(v *model.SiteAccountDailyBalance) { v.CollectedAt = v.ScheduledAt - 1 },
		func(v *model.SiteAccountDailyBalance) { v.Balance = nil },
		func(v *model.SiteAccountDailyBalance) { v.Currency = " " },
		func(v *model.SiteAccountDailyBalance) { v.Currency = strings.Repeat("X", 17) },
		func(v *model.SiteAccountDailyBalance) { v.ErrorCode = strings.Repeat("X", 65) },
		func(v *model.SiteAccountDailyBalance) { n := math.NaN(); v.Balance = &n },
		func(v *model.SiteAccountDailyBalance) { n := math.Inf(1); v.Balance = &n },
		func(v *model.SiteAccountDailyBalance) { n := math.Inf(-1); v.Balance = &n },
		func(v *model.SiteAccountDailyBalance) { v.Status = "unknown" },
		func(v *model.SiteAccountDailyBalance) { v.Status = "failed" },
	} {
		invalid := *base
		change(&invalid)
		if finished, err := store.CompleteSiteAccountBalanceSample(ctx, &invalid); err == nil || finished {
			t.Fatalf("accepted invalid completion %+v: finished=%v err=%v", invalid, finished, err)
		}
	}
	if finished, err := store.CompleteSiteAccountBalanceSample(ctx, nil); err == nil || finished {
		t.Fatalf("nil completion=%v err=%v", finished, err)
	}
	rows, err := store.ListSiteAccountBalanceSamples(ctx, base.LocalDay, base.LocalDay)
	if err != nil || len(rows) != 1 || rows[0].Status != "unknown" || rows[0].Balance != nil {
		t.Fatalf("invalid writes changed the claim: %+v err=%v", rows, err)
	}
	zero := 0.0
	base.Balance = &zero
	if finished, err := store.CompleteSiteAccountBalanceSample(ctx, base); err != nil || !finished {
		t.Fatalf("real zero at scheduled_at rejected: finished=%v err=%v", finished, err)
	}
}

func TestBalanceSamplesWritesRespectTransactionRollback(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	primary := store.(*sqlstore.SQLStore)
	ctx := context.Background()
	old := balanceSampleFixture(t, id, "2026-08-29", 9)
	if _, err := store.CreateSiteAccountBalanceSample(ctx, old); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("synthetic rollback")
	err := primary.WithSerializableTransaction(ctx, func(ctx context.Context, tx *sqlstore.SQLStore) error {
		sample := balanceSampleFixture(t, id, "2026-11-28", 9)
		if claimed, err := tx.CreateSiteAccountBalanceSample(ctx, sample); err != nil || !claimed {
			t.Fatalf("transaction claim=%v err=%v", claimed, err)
		}
		amount := 15.0
		sample.Status, sample.Balance, sample.CollectedAt = "success", &amount, sample.ScheduledAt+1
		if finished, err := tx.CompleteSiteAccountBalanceSample(ctx, sample); err != nil || !finished {
			t.Fatalf("transaction completion=%v err=%v", finished, err)
		}
		if removed, err := tx.DeleteSiteAccountBalanceSamplesBefore(ctx, "2026-08-30", 10); err != nil || removed != 1 {
			t.Fatalf("transaction cleanup=%d err=%v", removed, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error=%v", err)
	}
	rows, err := store.ListSiteAccountBalanceSamples(ctx, "", "")
	if err != nil || len(rows) != 1 || rows[0].ScheduledAt != old.ScheduledAt || rows[0].Status != "unknown" {
		t.Fatalf("rollback leaked interval writes: %+v err=%v", rows, err)
	}
}

func TestBalanceSamplesCompletionWriteErrorKeepsUnknown(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	primary := store.(*sqlstore.SQLStore)
	ctx := context.Background()
	sample := balanceSampleFixture(t, id, "2026-11-28", 9)
	if _, err := store.CreateSiteAccountBalanceSample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.ExecContext(ctx, `CREATE TRIGGER reject_balance_sample BEFORE UPDATE ON site_account_balance_samples BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	amount := 15.0
	sample.Status, sample.Balance, sample.CollectedAt = "success", &amount, sample.ScheduledAt+1
	if finished, err := store.CompleteSiteAccountBalanceSample(ctx, sample); err == nil || finished {
		t.Fatalf("write error hidden: finished=%v err=%v", finished, err)
	}
	rows, err := store.ListSiteAccountBalanceSamples(ctx, "", "")
	if err != nil || len(rows) != 1 || rows[0].Status != "unknown" || rows[0].Balance != nil || rows[0].CollectedAt != 0 {
		t.Fatalf("failed write changed observation: %+v err=%v", rows, err)
	}
}

func TestBalanceSamplesCleanupIsBoundedAndLeavesDailyHistory(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	for hour := 9; hour <= 11; hour++ {
		if claimed, err := store.CreateSiteAccountBalanceSample(ctx, balanceSampleFixture(t, id, "2026-08-29", hour)); err != nil || !claimed {
			t.Fatalf("old claim=%v err=%v", claimed, err)
		}
	}
	for _, day := range []string{"2026-08-30", "2026-11-28"} {
		if _, err := store.CreateSiteAccountBalanceSample(ctx, balanceSampleFixture(t, id, day, 9)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateSiteAccountDailyBalance(ctx, balanceSampleFixture(t, id, "2026-08-29", 9)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct {
		day   string
		limit int
	}{{"", 2}, {"bad", 2}, {"2026-02-30", 2}, {"2026-08-30", 0}, {"2026-08-30", -1}} {
		if removed, err := store.DeleteSiteAccountBalanceSamplesBefore(ctx, invalid.day, invalid.limit); err == nil || removed != 0 {
			t.Fatalf("invalid cleanup removed=%d err=%v", removed, err)
		}
	}
	for _, want := range []int64{2, 1, 0} {
		if removed, err := store.DeleteSiteAccountBalanceSamplesBefore(ctx, "2026-08-30", 2); err != nil || removed != want {
			t.Fatalf("cleanup removed=%d want=%d err=%v", removed, want, err)
		}
	}
	rows, err := store.ListSiteAccountBalanceSamples(ctx, "2026-08-30", "2026-08-30")
	if err != nil || len(rows) != 1 || rows[0].LocalDay != "2026-08-30" {
		t.Fatalf("inclusive cutoff-day history=%+v err=%v", rows, err)
	}
	rows, err = store.ListSiteAccountBalanceSamples(ctx, "", "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("cleanup crossed cutoff: %+v err=%v", rows, err)
	}
	if daily, err := store.ListSiteAccountDailyBalances(ctx, "", ""); err != nil || len(daily) != 1 {
		t.Fatalf("cleanup touched legacy daily history: %+v err=%v", daily, err)
	}
}

func TestBalanceSamplesCleanupWriteFailureRollsBackAllBatches(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	primary := store.(*sqlstore.SQLStore)
	ctx := context.Background()
	if err := primary.WithSerializableTransaction(ctx, func(ctx context.Context, tx *sqlstore.SQLStore) error {
		for index := 0; index < 401; index++ {
			sample := balanceSampleFixture(t, id, "2026-08-29", 9)
			sample.ScheduledAt += int64(index)
			if claimed, err := tx.CreateSiteAccountBalanceSample(ctx, sample); err != nil || !claimed {
				t.Fatalf("seed claim=%v err=%v", claimed, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.ExecContext(ctx, `CREATE TRIGGER reject_last_balance_sample BEFORE DELETE ON site_account_balance_samples WHEN OLD.scheduled_at=(SELECT MAX(scheduled_at) FROM site_account_balance_samples) BEGIN SELECT RAISE(ABORT, 'synthetic cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.DeleteSiteAccountBalanceSamplesBefore(ctx, "2026-08-30", 401); err == nil || removed != 0 {
		t.Fatalf("cleanup write error hidden: removed=%d err=%v", removed, err)
	}
	rows, err := store.ListSiteAccountBalanceSamples(ctx, "", "")
	if err != nil || len(rows) != 401 {
		t.Fatalf("failed cleanup leaked partial deletion: rows=%d err=%v", len(rows), err)
	}
	if _, err := primary.ExecContext(ctx, `DROP TRIGGER reject_last_balance_sample`); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.DeleteSiteAccountBalanceSamplesBefore(ctx, "2026-08-30", 401); err != nil || removed != 401 {
		t.Fatalf("multi-batch cleanup removed=%d err=%v", removed, err)
	}
}

func TestBalanceSamplesMigrationIsAdditiveAndIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := migrateSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlstore.NewSQLStore(db, "sqlite")
	legacy := balanceSampleFixture(t, 1, "2026-11-28", 9)
	if claimed, err := store.CreateSiteAccountDailyBalance(ctx, legacy); err != nil || !claimed {
		t.Fatalf("legacy daily claim=%v err=%v", claimed, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE site_account_balance_samples`); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if err := migrateSQLite(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	if rows, err := store.ListSiteAccountBalanceSamples(ctx, "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("migration invented interval observations: %+v err=%v", rows, err)
	}
	if rows, err := store.ListSiteAccountDailyBalances(ctx, "", ""); err != nil || len(rows) != 1 || rows[0].ScheduledAt != legacy.ScheduledAt || rows[0].Status != "unknown" {
		t.Fatalf("migration rewrote legacy history: %+v err=%v", rows, err)
	}
	var indexedColumn string
	if err := db.QueryRowContext(ctx, `SELECT name FROM pragma_index_info('idx_site_balance_samples_day')`).Scan(&indexedColumn); err != nil || indexedColumn != "local_day" {
		t.Fatalf("day index=%q err=%v", indexedColumn, err)
	}
	for name, dialect := range map[string]Dialect{"sqlite": DialectSQLite, "mysql": DialectMySQL, "postgres": DialectPostgres} {
		t.Run(name+" DDL", func(t *testing.T) {
			ddl := buildDDL(schema.DefineSiteAccountBalanceSamplesTable(), dialect)
			if !strings.Contains(ddl, "PRIMARY KEY (site_account_id, scheduled_at)") || strings.Contains(ddl, "REFERENCES") {
				t.Fatalf("slot identity/retention contract missing from DDL: %s", ddl)
			}
			if name == "postgres" && !strings.Contains(ddl, "balance DOUBLE PRECISION") {
				t.Fatalf("Postgres balance type is not portable: %s", ddl)
			}
		})
	}
}

func TestBalanceSamplesMigrationDefaultsAndPreservesPreferences(t *testing.T) {
	for _, tc := range []struct {
		name    string
		upgrade bool
		enabled string
		at      string
	}{
		{name: "fresh installation", enabled: "true", at: "00:05"},
		{name: "upgrade with sampling disabled", upgrade: true, enabled: "false", at: "07:35"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			if err := migrateSQLite(ctx, db); err != nil {
				t.Fatal(err)
			}
			store := sqlstore.NewSQLStore(db, "sqlite")
			if tc.upgrade {
				if err := store.BatchUpdateSettings(ctx, map[string]string{
					"site_daily_balance_enabled": tc.enabled,
					"site_daily_balance_time":    tc.at,
				}); err != nil {
					t.Fatal(err)
				}
				// Model an existing installation before interval sampling was added.
				if err := store.DeleteSetting(ctx, "site_balance_sample_interval_minutes"); err != nil {
					t.Fatal(err)
				}
				if _, err := store.GetSetting(ctx, "site_balance_sample_interval_minutes"); !errors.Is(err, model.ErrSettingNotFound) {
					t.Fatalf("pre-interval setting should be absent: err=%v", err)
				}
			}
			for pass := 0; pass < 3; pass++ {
				// Check fresh defaults before rerunning, and upgrade defaults after migration.
				if tc.upgrade || pass > 0 {
					if err := migrateSQLite(ctx, db); err != nil {
						t.Fatal(err)
					}
				}
				for _, want := range []struct {
					key          string
					value        string
					defaultValue string
					valueType    string
				}{
					{"site_balance_sample_interval_minutes", "60", "60", "int"},
					{"site_daily_balance_enabled", tc.enabled, "true", "bool"},
					{"site_daily_balance_time", tc.at, "00:05", "string"},
				} {
					got, err := store.GetSetting(ctx, want.key)
					if err != nil {
						t.Fatalf("pass %d setting %s: %v", pass, want.key, err)
					}
					if got.Value != want.value || got.DefaultValue != want.defaultValue || got.ValueType != want.valueType {
						t.Fatalf("pass %d setting %s: value=%q default=%q type=%q; want value=%q default=%q type=%q", pass, want.key, got.Value, got.DefaultValue, got.ValueType, want.value, want.defaultValue, want.valueType)
					}
				}
			}
		})
	}
}

func TestBalanceSamplesSurviveAccountAndSiteDeletionWithForeignKeys(t *testing.T) {
	for _, target := range []string{"account", "site"} {
		t.Run(target, func(t *testing.T) {
			db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "interval-history.db"))+"?_pragma=foreign_keys(1)")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			db.SetMaxOpenConns(1)
			ctx := context.Background()
			if err := migrateSQLite(ctx, db); err != nil {
				t.Fatal(err)
			}
			var foreignKeys int
			if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
				t.Fatalf("foreign_keys=%d err=%v", foreignKeys, err)
			}
			store := sqlstore.NewSQLStore(db, "sqlite")
			site, err := store.CreateSite(ctx, &model.Site{Name: "interval", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://example.invalid", Timezone: "Asia/Shanghai", TagsJSON: "[]"})
			if err != nil {
				t.Fatal(err)
			}
			account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "interval", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: "synthetic", CredentialKeyVersion: "test"})
			if err != nil {
				t.Fatal(err)
			}
			sample := balanceSampleFixture(t, account.ID, "2026-11-28", 9)
			if _, err := store.CreateSiteAccountBalanceSample(ctx, sample); err != nil {
				t.Fatal(err)
			}
			if target == "account" {
				err = store.DeleteSiteAccount(ctx, account.ID)
			} else {
				err = store.DeleteSite(ctx, site.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, err := store.ListSiteAccountBalanceSamples(ctx, "", "")
			if err != nil || len(rows) != 1 || rows[0].SiteAccountID != account.ID {
				t.Fatalf("%s deletion erased history: %+v err=%v", target, rows, err)
			}
		})
	}
}

func TestHybridBalanceSamplesDelegateOnlyToPrimary(t *testing.T) {
	primary, replica := createTestSQLiteStore(t), createTestSQLiteStore(t)
	t.Cleanup(func() { _ = primary.Close(); _ = replica.Close() })
	hybrid := &HybridStore{mysql: primary, sqlite: replica}
	ctx := context.Background()
	sample := balanceSampleFixture(t, 1, "2026-08-29", 9)
	if claimed, err := hybrid.CreateSiteAccountBalanceSample(ctx, sample); err != nil || !claimed {
		t.Fatalf("hybrid claim=%v err=%v", claimed, err)
	}
	if rows, err := replica.ListSiteAccountBalanceSamples(ctx, "", ""); err != nil || len(rows) != 0 {
		t.Fatalf("claim unexpectedly touched replica: %+v err=%v", rows, err)
	}
	if _, err := replica.CreateSiteAccountBalanceSample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	amount := 12.5
	sample.Status, sample.Balance, sample.CollectedAt = "success", &amount, sample.ScheduledAt+1
	if finished, err := hybrid.CompleteSiteAccountBalanceSample(ctx, sample); err != nil || !finished {
		t.Fatalf("hybrid completion=%v err=%v", finished, err)
	}
	rows, err := hybrid.ListSiteAccountBalanceSamples(ctx, "", "")
	if err != nil || len(rows) != 1 || rows[0].Status != "success" || rows[0].Balance == nil || *rows[0].Balance != amount {
		t.Fatalf("hybrid read stale replica: %+v err=%v", rows, err)
	}
	if removed, err := hybrid.DeleteSiteAccountBalanceSamplesBefore(ctx, "2026-08-30", 1); err != nil || removed != 1 {
		t.Fatalf("hybrid cleanup=%d err=%v", removed, err)
	}
	if rows, err := replica.ListSiteAccountBalanceSamples(ctx, "", ""); err != nil || len(rows) != 1 || rows[0].Status != "unknown" {
		t.Fatalf("primary mutations changed replica: %+v err=%v", rows, err)
	}
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := hybrid.ListSiteAccountBalanceSamples(ctx, "", ""); err == nil {
		t.Fatal("primary read failure was hidden by replica fallback")
	}
}

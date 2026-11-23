package storage

import (
	"context"
	"errors"
	"testing"
)

type conditionalSettingStore interface {
	CompareAndSwapSetting(ctx context.Context, key, expectedValue, newValue string) (bool, error)
}

func TestSQLSettingConditionalUpdatePreservesConcurrentValue(t *testing.T) {
	store, err := CreateSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cas, ok := store.(conditionalSettingStore)
	if !ok {
		t.Fatal("SQL store does not expose conditional system-setting updates")
	}
	ctx := context.Background()
	if err := store.UpdateSetting(ctx, "model_alias_groups", "initial"); err != nil {
		t.Fatal(err)
	}
	updated, err := cas.CompareAndSwapSetting(ctx, "model_alias_groups", "initial", "reconciled")
	if err != nil || !updated {
		t.Fatalf("matching CAS updated=%t err=%v", updated, err)
	}
	if err := store.UpdateSetting(ctx, "model_alias_groups", "user-edited"); err != nil {
		t.Fatal(err)
	}
	updated, err = cas.CompareAndSwapSetting(ctx, "model_alias_groups", "initial", "stale-reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale CAS reported success")
	}
	setting, err := store.GetSetting(ctx, "model_alias_groups")
	if err != nil {
		t.Fatal(err)
	}
	if setting.Value != "user-edited" {
		t.Fatalf("setting=%q, want concurrent user value", setting.Value)
	}
	if err := store.UpdateSetting(ctx, "model_alias_groups", "CaseSensitiveAlias"); err != nil {
		t.Fatal(err)
	}
	updated, err = cas.CompareAndSwapSetting(ctx, "model_alias_groups", "casesensitivealias", "wrong-case-overwrite")
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("case-insensitive CAS matched a case-different persisted value")
	}
	setting, err = store.GetSetting(ctx, "model_alias_groups")
	if err != nil {
		t.Fatal(err)
	}
	if setting.Value != "CaseSensitiveAlias" {
		t.Fatalf("setting=%q, want exact persisted case preserved", setting.Value)
	}
}

func TestHybridSettingConditionalUpdatePreservesConcurrentValue(t *testing.T) {
	mysql := createTestSQLiteStore(t)
	sqlite := createTestSQLiteStore(t)
	hybrid := NewHybridStore(sqlite, mysql)
	t.Cleanup(func() { _ = hybrid.Close() })
	cas, ok := any(hybrid).(conditionalSettingStore)
	if !ok {
		t.Fatal("hybrid store does not expose conditional system-setting updates")
	}
	ctx := context.Background()
	if err := hybrid.UpdateSetting(ctx, "model_alias_groups", "initial"); err != nil {
		t.Fatal(err)
	}
	updated, err := cas.CompareAndSwapSetting(ctx, "model_alias_groups", "initial", "reconciled")
	if err != nil || !updated {
		t.Fatalf("matching CAS updated=%t err=%v", updated, err)
	}
	setting, err := hybrid.GetSetting(ctx, "model_alias_groups")
	if err != nil {
		t.Fatal(err)
	}
	if setting.Value != "reconciled" {
		t.Fatalf("hybrid read after CAS=%q, want reconciled", setting.Value)
	}
	if err := hybrid.UpdateSetting(ctx, "model_alias_groups", "user-edited"); err != nil {
		t.Fatal(err)
	}
	updated, err = cas.CompareAndSwapSetting(ctx, "model_alias_groups", "initial", "stale-reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale hybrid CAS reported success")
	}
	setting, err = hybrid.GetSetting(ctx, "model_alias_groups")
	if err != nil {
		t.Fatal(err)
	}
	if setting.Value != "user-edited" {
		t.Fatalf("hybrid setting=%q, want concurrent user value", setting.Value)
	}
}

func TestHybridSettingConditionalUpdateReportsPrimaryCommitWhenReplicaFails(t *testing.T) {
	mysql := createTestSQLiteStore(t)
	sqlite := createTestSQLiteStore(t)
	hybrid := NewHybridStore(sqlite, mysql)
	t.Cleanup(func() { _ = hybrid.Close() })
	cas, ok := any(hybrid).(conditionalSettingStore)
	if !ok {
		t.Fatal("hybrid store does not expose conditional system-setting updates")
	}
	ctx := context.Background()
	if err := hybrid.UpdateSetting(ctx, "model_alias_groups", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Close(); err != nil {
		t.Fatal(err)
	}
	updated, err := cas.CompareAndSwapSetting(ctx, "model_alias_groups", "initial", "reconciled")
	if !updated || err == nil {
		t.Fatalf("conditional update updated=%t err=%v, want primary commit with replica failure", updated, err)
	}
	setting, err := mysql.GetSetting(ctx, "model_alias_groups")
	if err != nil {
		t.Fatal(err)
	}
	if setting.Value != "reconciled" {
		t.Fatalf("primary setting=%q, want reconciled", setting.Value)
	}
}

func TestConfigurationTransactionConditionalSettingMirrorsOnlyAfterCommit(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	if err := hybrid.UpdateSetting(ctx, "model_alias_groups", "initial"); err != nil {
		t.Fatal(err)
	}
	abort := errors.New("abort conditional fixture")
	for _, commit := range []bool{false, true} {
		err := WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
			cas, ok := store.(conditionalSettingStore)
			if !ok {
				return errors.New("transaction must expose conditional settings")
			}
			updated, err := cas.CompareAndSwapSetting(ctx, "model_alias_groups", "initial", "reconciled")
			if err != nil || !updated {
				t.Fatalf("transaction CAS updated=%t err=%v", updated, err)
			}
			cached, err := cache.GetSetting(ctx, "model_alias_groups")
			if err != nil || cached.Value != "initial" {
				t.Fatalf("cache changed before commit: %v %v", cached, err)
			}
			if !commit {
				return abort
			}
			return nil
		})
		if !commit && !errors.Is(err, abort) || commit && err != nil {
			t.Fatalf("commit=%t err=%v", commit, err)
		}
		want := "initial"
		if commit {
			want = "reconciled"
		}
		for _, store := range []Store{primary, cache} {
			setting, err := store.GetSetting(ctx, "model_alias_groups")
			if err != nil || setting.Value != want {
				t.Fatalf("commit=%t setting=%v err=%v want %q", commit, setting, err, want)
			}
		}
	}
}

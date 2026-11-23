package sql_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	"github.com/yzgolden86/PivotFlow/internal/storage/schema"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func TestRequestTraceExactModelProtocolAndTokenStorageFilters(t *testing.T) {
	store, err := storage.CreateSQLiteStore(t.TempDir() + "/exact-history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	at := time.Now().Add(-time.Second).UnixMilli()
	names := []string{"name%_'quote", strings.Repeat("模", 191)}
	for i, name := range names {
		prefix := strings.Repeat("r", i+1)
		rows := []*model.RequestTrace{
			{RequestID: prefix + "exact", StartedAt: at, AuthTokenID: 7, Model: name, ClientProtocol: "openai", Transport: "http", Outcome: "success", FinalStatus: 200},
			{RequestID: prefix + "prefix", StartedAt: at, AuthTokenID: 7, Model: name[:len(name)-3], ClientProtocol: "openai", Transport: "http", Outcome: "error", FinalStatus: 500},
			{RequestID: prefix + "protocol", StartedAt: at, AuthTokenID: 7, Model: name, ClientProtocol: "codex", Transport: "http", Outcome: "error", FinalStatus: 500},
			{RequestID: prefix + "token", StartedAt: at, AuthTokenID: 8, Model: name, ClientProtocol: "openai", Transport: "http", Outcome: "error", FinalStatus: 500},
		}
		if err := store.BatchAddRequestTraces(ctx, rows); err != nil {
			t.Fatal(err)
		}
		id := int64(7)
		filter := &model.RequestTraceFilter{Model: name, ClientProtocol: "openai", AuthTokenID: &id}
		got, count, err := store.ListRequestTraces(ctx, time.Time{}, time.Now(), 50, 0, filter)
		if err != nil || count != 1 || len(got) != 1 || got[0].RequestID != prefix+"exact" {
			t.Fatalf("exact list %d: %v count=%d err=%v", i, got, count, err)
		}
		stats, err := store.GetRequestTraceStats(ctx, time.Time{}, time.Now(), filter)
		if err != nil || stats.Requests != 1 || stats.Successes != 1 || stats.Errors != 0 {
			t.Fatalf("exact stats %d: %+v %v", i, stats, err)
		}
	}
}

func TestRequestHistoryExactFiltersIgnoreCaseInsensitiveColumnCollation(t *testing.T) {
	store, err := storage.CreateSQLiteStore(t.TempDir() + "/nocase-history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sqlDB := store.(*sqlstore.SQLStore)
	ctx := context.Background()
	// Exercise real SQL against case-insensitive columns, rather than assuming
	// the production database's default collation is byte-exact.
	for _, table := range []*schema.TableBuilder{schema.DefineRequestTracesTable(), schema.DefineLogsTable()} {
		if _, err := sqlDB.ExecContext(ctx, "DROP TABLE "+table.Name()); err != nil {
			t.Fatal(err)
		}
		ddl := strings.ReplaceAll(table.BuildSQLite(), "model TEXT ", "model TEXT COLLATE NOCASE ")
		ddl = strings.ReplaceAll(ddl, "client_protocol TEXT ", "client_protocol TEXT COLLATE NOCASE ")
		if _, err := sqlDB.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Second)
	traces := []*model.RequestTrace{
		{RequestID: "nocase-exact", StartedAt: at.UnixMilli(), AuthTokenID: 7, Model: "Model-A", ClientProtocol: "openai", Transport: "http", Outcome: "success", FinalStatus: 200},
		{RequestID: "nocase-model", StartedAt: at.UnixMilli(), AuthTokenID: 7, Model: "model-a", ClientProtocol: "openai", Transport: "http", Outcome: "error", FinalStatus: 500},
		{RequestID: "nocase-protocol", StartedAt: at.UnixMilli(), AuthTokenID: 7, Model: "Model-A", ClientProtocol: "openai", Transport: "http", Outcome: "error", FinalStatus: 500},
	}
	if err := store.BatchAddRequestTraces(ctx, traces); err != nil {
		t.Fatal(err)
	}
	// Model a legacy/imported protocol spelling without normalizing its identity.
	if _, err := sqlDB.ExecContext(ctx, "UPDATE request_traces SET client_protocol=? WHERE request_id=?", "OPENAI", "nocase-protocol"); err != nil {
		t.Fatal(err)
	}
	id := int64(7)
	filter := &model.RequestTraceFilter{Model: "Model-A", ClientProtocol: "openai", AuthTokenID: &id}
	rows, count, err := store.ListRequestTraces(ctx, time.Time{}, time.Now(), 50, 0, filter)
	if err != nil || count != 1 || len(rows) != 1 || rows[0].RequestID != "nocase-exact" {
		t.Errorf("NOCASE history list=%v count=%d err=%v, want one exact identity", rows, count, err)
	}
	stats, err := store.GetRequestTraceStats(ctx, time.Time{}, time.Now(), filter)
	if err != nil || stats.Requests != 1 || stats.Successes != 1 || stats.Errors != 0 {
		t.Errorf("NOCASE history stats=%+v err=%v, want exact identity", stats, err)
	}
	channel, err := store.CreateConfig(ctx, &model.Config{Name: "history collation fixture", Enabled: true,
		URLs: model.ChannelURLs{{URL: "https://history.example.invalid", Protocols: []string{"openai"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range traces {
		client := row.ClientProtocol
		if row.RequestID == "nocase-protocol" {
			client = "OPENAI"
		}
		if err := store.AddLog(ctx, &model.LogEntry{
			Time: model.JSONTime{Time: at}, ChannelID: channel.ID, Model: row.Model,
			ClientProtocol: client, AuthTokenID: id, StatusCode: row.FinalStatus,
			LogSource: model.LogSourceProxy, RequestID: row.RequestID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	logFilter := &model.LogFilter{Model: "Model-A", ClientProtocol: "openai", AuthTokenID: &id}
	logs, count, err := store.ListLogsRangeWithCount(ctx, time.Time{}, time.Now(), 50, 0, logFilter)
	if err != nil || count != 1 || len(logs) != 1 || logs[0].RequestID != "nocase-exact" {
		t.Errorf("NOCASE logs=%v count=%d err=%v, want one exact identity", logs, count, err)
	}
	lite, err := store.GetStatsLite(ctx, time.Time{}, time.Now(), logFilter)
	var attempts int
	for _, item := range lite {
		attempts += item.Total
	}
	if err != nil || attempts != 1 {
		t.Errorf("NOCASE diagnostic attempts=%d err=%v, want one exact identity", attempts, err)
	}
}

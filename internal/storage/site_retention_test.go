package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// The sweep relies on each pass being bounded, so that a large backlog is
// removed a chunk at a time instead of in one long write lock. That contract
// lives here: the caller loops until a pass comes back short.
func TestDeleteFinishedSiteTasksHonoursTheLimit(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/prune.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	old := now - int64((48 * time.Hour).Milliseconds())

	for i := 0; i < 5; i++ {
		task := &model.SiteTask{
			ID: fmt.Sprintf("st_%d", i), Kind: "refresh", Status: model.SiteTaskStatusSuccess,
			ProgressJSON: "{}", CreatedAt: old, FinishedAt: old + int64(i),
		}
		if err := store.CreateSiteTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}

	for pass, want := range []int64{2, 2, 1, 0} {
		removed, err := store.DeleteFinishedSiteTasks(ctx, now, 2)
		if err != nil {
			t.Fatal(err)
		}
		if removed != want {
			t.Fatalf("pass %d removed %d rows, want %d", pass, removed, want)
		}
	}
}

// Only rows past the cutoff may go: a task that finished after it is still
// recent history, and one that never finished is not history at all.
func TestDeleteFinishedSiteTasksKeepsRecentAndUnfinishedRows(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/prune-cutoff.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	old := now - int64((48 * time.Hour).Milliseconds())
	cutoff := now - int64(time.Hour.Milliseconds())

	seed := func(id, status string, finishedAt int64) {
		t.Helper()
		task := &model.SiteTask{ID: id, Kind: "refresh", Status: status, ProgressJSON: "{}", CreatedAt: old, FinishedAt: finishedAt}
		if err := store.CreateSiteTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	seed("st_stale", model.SiteTaskStatusSuccess, old)
	seed("st_recent", model.SiteTaskStatusSuccess, now)
	seed("st_running", model.SiteTaskStatusRunning, old)
	seed("st_queued", model.SiteTaskStatusQueued, old)

	removed, err := store.DeleteFinishedSiteTasks(ctx, cutoff, 100)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want only the stale terminal row", removed)
	}
	if _, err := store.GetSiteTask(ctx, "st_stale"); err == nil {
		t.Fatal("stale terminal task survived")
	}
	for _, id := range []string{"st_recent", "st_running", "st_queued"} {
		if _, err := store.GetSiteTask(ctx, id); err != nil {
			t.Fatalf("task %s was removed: %v", id, err)
		}
	}
}

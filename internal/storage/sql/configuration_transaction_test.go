package sql_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

type commitErrorDriver struct {
	err error
}

func (d *commitErrorDriver) Open(string) (driver.Conn, error) {
	return &commitErrorConn{err: d.err}, nil
}

type commitErrorConnector struct {
	driver *commitErrorDriver
}

func (c *commitErrorConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open("")
}

func (c *commitErrorConnector) Driver() driver.Driver {
	return c.driver
}

type commitErrorConn struct {
	err error
}

func (c *commitErrorConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by the commit error test driver")
}

func (c *commitErrorConn) Close() error {
	return nil
}

func (c *commitErrorConn) Begin() (driver.Tx, error) {
	return &commitErrorTx{err: c.err}, nil
}

func (c *commitErrorConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &commitErrorTx{err: c.err}, nil
}

type commitErrorTx struct {
	err error
}

func (tx *commitErrorTx) Commit() error {
	return tx.err
}

func (tx *commitErrorTx) Rollback() error {
	return nil
}

type failFirstCommitDriver struct {
	commits atomic.Int32
}

func (d *failFirstCommitDriver) Open(string) (driver.Conn, error) {
	return &failFirstCommitConn{driver: d}, nil
}

type failFirstCommitConnector struct {
	driver *failFirstCommitDriver
}

func (c *failFirstCommitConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open("")
}

func (c *failFirstCommitConnector) Driver() driver.Driver {
	return c.driver
}

type failFirstCommitConn struct {
	driver *failFirstCommitDriver
}

func (c *failFirstCommitConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by the fail-first commit test driver")
}

func (c *failFirstCommitConn) Close() error {
	return nil
}

func (c *failFirstCommitConn) Begin() (driver.Tx, error) {
	return &failFirstCommitTx{driver: c.driver}, nil
}

func (c *failFirstCommitConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &failFirstCommitTx{driver: c.driver}, nil
}

type failFirstCommitTx struct {
	driver *failFirstCommitDriver
}

func (tx *failFirstCommitTx) Commit() error {
	if tx.driver.commits.Add(1) == 1 {
		return errors.New("database is locked during commit")
	}
	return nil
}

func (tx *failFirstCommitTx) Rollback() error {
	return nil
}

func TestWithSerializableTransactionNestedWritesRollBackTogether(t *testing.T) {
	store := newTestStore(t, "config_tx_rollback.db").(*sqlstore.SQLStore)
	ctx := context.Background()
	createTestChannel(t, ctx, store, "seed-channel")

	abort := errors.New("abort configuration transaction")
	err := store.WithSerializableTransaction(ctx, func(ctx context.Context, tx *sqlstore.SQLStore) error {
		created, err := tx.CreateConfig(ctx, &model.Config{
			Name:         "rolled-back-channel",
			URLs:         model.ChannelURLs{{URL: "https://rollback.example.com"}},
			Enabled:      true,
			ModelEntries: []model.ModelEntry{{Model: "gpt-rollback"}},
		})
		if err != nil {
			return err
		}
		if err := tx.CreateAPIKeysBatch(ctx, []*model.APIKey{{
			ChannelID:   created.ID,
			KeyIndex:    0,
			APIKey:      "sk-rollback",
			KeyStrategy: model.KeyStrategySequential,
		}}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("error=%v, want %v", err, abort)
	}

	configs, err := store.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("list configs after rollback: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "seed-channel" {
		t.Fatalf("rollback left unexpected configs: %+v", configs)
	}
	keys, err := store.GetAllAPIKeys(ctx)
	if err != nil {
		t.Fatalf("list api keys after rollback: %v", err)
	}
	if countAPIKeys(keys) != 0 {
		t.Fatalf("rollback left api keys: %+v", keys)
	}
}

func TestWithSerializableTransactionReadsSeeOwnWritesAndCommit(t *testing.T) {
	store := newTestStore(t, "config_tx_commit.db").(*sqlstore.SQLStore)
	ctx := context.Background()

	var channelID int64
	err := store.WithSerializableTransaction(ctx, func(ctx context.Context, tx *sqlstore.SQLStore) error {
		created, err := tx.CreateConfig(ctx, &model.Config{
			Name:         "transaction-channel",
			URLs:         model.ChannelURLs{{URL: "https://transaction.example.com"}},
			Enabled:      true,
			ModelEntries: []model.ModelEntry{{Model: "gpt-tx"}},
		})
		if err != nil {
			return err
		}
		channelID = created.ID

		configs, err := tx.ListConfigs(ctx)
		if err != nil {
			return err
		}
		if len(configs) != 1 || configs[0].ID != channelID {
			t.Fatalf("transaction read did not see own write: %+v", configs)
		}
		if err := tx.CreateAPIKeysBatch(ctx, []*model.APIKey{{
			ChannelID:   channelID,
			KeyIndex:    0,
			APIKey:      "sk-commit",
			KeyStrategy: model.KeyStrategySequential,
		}}); err != nil {
			return err
		}
		keys, err := tx.GetAPIKeys(ctx, channelID)
		if err != nil {
			return err
		}
		if len(keys) != 1 {
			t.Fatalf("nested transaction read keys=%d, want 1", len(keys))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("configuration transaction: %v", err)
	}

	if _, err := store.GetConfig(ctx, channelID); err != nil {
		t.Fatalf("committed config missing: %v", err)
	}
	keys, err := store.GetAPIKeys(ctx, channelID)
	if err != nil {
		t.Fatalf("committed api keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("committed keys=%d, want 1", len(keys))
	}
}

func TestWithSerializableTransactionNestedBatchImportAndDeleteShareTransaction(t *testing.T) {
	store := newTestStore(t, "config_tx_import.db").(*sqlstore.SQLStore)
	ctx := context.Background()

	var channelID int64
	err := store.WithSerializableTransaction(ctx, func(ctx context.Context, tx *sqlstore.SQLStore) error {
		channels := []*model.ChannelWithKeys{{
			Config: &model.Config{
				Name:         "imported-in-transaction",
				URLs:         model.ChannelURLs{{URL: "https://import-tx.example.com"}},
				Enabled:      true,
				ModelEntries: []model.ModelEntry{{Model: "gpt-import"}},
			},
			APIKeys: []model.APIKey{
				{KeyIndex: 0, APIKey: "import-test-key-zero", KeyStrategy: model.KeyStrategySequential},
				{KeyIndex: 1, APIKey: "import-test-key-one", KeyStrategy: model.KeyStrategySequential},
			},
		}}
		created, updated, err := tx.ImportChannelBatch(ctx, channels)
		if err != nil {
			return err
		}
		if created != 1 || updated != 0 {
			t.Fatalf("import created=%d updated=%d", created, updated)
		}
		channelID = channels[0].Config.ID
		keys, err := tx.GetAPIKeys(ctx, channelID)
		if err != nil {
			return err
		}
		if len(keys) != 2 {
			t.Fatalf("imported keys=%d, want 2", len(keys))
		}
		if err := tx.DeleteAllAPIKeys(ctx, channelID); err != nil {
			return err
		}
		keys, err = tx.GetAPIKeys(ctx, channelID)
		if err != nil {
			return err
		}
		if len(keys) != 0 {
			t.Fatalf("keys after delete=%d, want 0", len(keys))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("configuration transaction: %v", err)
	}
	if _, err := store.GetConfig(ctx, channelID); err != nil {
		t.Fatalf("imported channel missing after commit: %v", err)
	}
	keys, err := store.GetAPIKeys(ctx, channelID)
	if err != nil {
		t.Fatalf("read committed keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("committed keys=%d, want 0", len(keys))
	}
}

func TestWithSerializableTransactionRollbackDiscardsChannelTombstone(t *testing.T) {
	store := newTestStore(t, "config_tx_tombstone.db").(*sqlstore.SQLStore)
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "tombstone-rollback")

	abort := errors.New("abort channel deletion")
	err := store.WithSerializableTransaction(ctx, func(ctx context.Context, tx *sqlstore.SQLStore) error {
		if err := tx.DeleteConfig(ctx, channelID); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("error=%v, want %v", err, abort)
	}
	if _, err := store.GetConfig(ctx, channelID); err != nil {
		t.Fatalf("rolled-back channel missing: %v", err)
	}

	if err := store.AddLog(ctx, &model.LogEntry{
		Time:       model.JSONTime{Time: time.Now()},
		ChannelID:  channelID,
		Model:      "gpt-tombstone",
		StatusCode: 200,
	}); err != nil {
		t.Fatalf("add log after rollback: %v", err)
	}
	logs, err := store.ListLogs(ctx, time.Now().Add(-time.Hour), 10, 0, nil)
	if err != nil {
		t.Fatalf("list logs after rollback: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs after rollback=%d, want 1; a leaked tombstone dropped the row", len(logs))
	}
}

func TestWithSerializableTransactionReadsReplacedModels(t *testing.T) {
	store := newTestStore(t, "config_tx_models.db").(*sqlstore.SQLStore)
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "model-transaction")

	err := store.WithSerializableTransaction(ctx, func(ctx context.Context, tx *sqlstore.SQLStore) error {
		current, err := tx.GetConfig(ctx, channelID)
		if err != nil {
			return err
		}
		current.ModelEntries = []model.ModelEntry{{Model: "gpt-replaced"}}
		if _, err := tx.UpdateConfig(ctx, channelID, current); err != nil {
			return err
		}

		configs, err := tx.ListConfigs(ctx)
		if err != nil {
			return err
		}
		if len(configs) != 1 || len(configs[0].ModelEntries) != 1 || configs[0].ModelEntries[0].Model != "gpt-replaced" {
			t.Fatalf("transaction ListConfigs returned stale models: %+v", configs)
		}
		config, err := tx.GetConfig(ctx, channelID)
		if err != nil {
			return err
		}
		if len(config.ModelEntries) != 1 || config.ModelEntries[0].Model != "gpt-replaced" {
			t.Fatalf("transaction GetConfig returned stale models: %+v", config.ModelEntries)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read replaced models in transaction: %v", err)
	}
}

func TestWithSerializableTransactionMarksFailedCommitUnknown(t *testing.T) {
	commitErr := errors.New("synthetic commit failure")
	db := sql.OpenDB(&commitErrorConnector{driver: &commitErrorDriver{err: commitErr}})
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstore.NewSQLStore(db, "sqlite")
	ctx := context.Background()

	err := store.WithSerializableTransaction(ctx, func(context.Context, *sqlstore.SQLStore) error {
		return nil
	})
	if !storage.IsConfigurationCommitUnknown(err) {
		t.Fatalf("serializable transaction error=%v, want commit unknown", err)
	}
	if !errors.Is(err, sqlstore.ErrTransactionCommitUnknown) {
		t.Fatalf("commit unknown error does not wrap the typed sentinel: %v", err)
	}
	if !errors.Is(err, commitErr) {
		t.Fatalf("commit unknown error lost the driver cause: %v", err)
	}

	legacyErr := store.WithTransaction(ctx, func(*sql.Tx) error {
		return nil
	})
	if legacyErr == nil || !strings.Contains(legacyErr.Error(), "commit transaction:") {
		t.Fatalf("legacy transaction error=%v, want existing commit transaction message", legacyErr)
	}
	if storage.IsConfigurationCommitUnknown(legacyErr) {
		t.Fatalf("legacy WithTransaction surfaced configuration commit-unknown semantics: %v", legacyErr)
	}
}

func TestWithSerializableTransactionDoesNotRetryUnknownCommit(t *testing.T) {
	driver := &failFirstCommitDriver{}
	db := sql.OpenDB(&failFirstCommitConnector{driver: driver})
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstore.NewSQLStore(db, "sqlite")

	err := store.WithSerializableTransaction(context.Background(), func(context.Context, *sqlstore.SQLStore) error {
		return nil
	})
	if !storage.IsConfigurationCommitUnknown(err) {
		t.Fatalf("serializable transaction error=%v, want commit unknown", err)
	}
	if attempts := driver.commits.Load(); attempts != 1 {
		t.Fatalf("commit attempts=%d, want exactly 1", attempts)
	}
}

package app

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
	"modernc.org/sqlite"
)

// Wrap the real SQLite driver at one read boundary. A second real connection
// commits an edit after ListSites has read its rows and before ListAccounts
// starts. Without a shared snapshot, the resulting document mixes both edits.
type backupSnapshotDriver struct {
	hook  func() error
	fired atomic.Bool
}

type backupSnapshotConnection struct {
	driver.Conn
	owner *backupSnapshotDriver
}

type backupSnapshotRows struct {
	driver.Rows
	owner *backupSnapshotDriver
}

var backupSnapshotDriverSequence atomic.Uint64

func (d *backupSnapshotDriver) Open(name string) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &backupSnapshotConnection{Conn: conn, owner: d}, nil
}

func (c *backupSnapshotConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	if strings.Contains(query, " FROM sites WHERE deleted_at = 0 ") {
		return &backupSnapshotRows{Rows: rows, owner: c.owner}, nil
	}
	return rows, nil
}

func (r *backupSnapshotRows) Close() error {
	if err := r.Rows.Close(); err != nil {
		return err
	}
	if r.owner.fired.CompareAndSwap(false, true) {
		return r.owner.hook()
	}
	return nil
}

func TestBackupSnapshotConcurrentEditCannotMixSitesAndAccounts(t *testing.T) {
	t.Setenv("SQLITE_JOURNAL_MODE", "WAL")
	ctx := context.Background()
	primary := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "snapshot.db"), "0123456789abcdef0123456789abcdef", "snapshot")
	site, account := restoreAccountFixture(t, primary, "Snapshot before", "https://synthetic-snapshot.invalid", model.SitePlatformOpenAICompatible, "before", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-snapshot-management"})
	observed := &backupSnapshotDriver{hook: func() error {
		return storage.WithConfigurationTransaction(ctx, primary.store, func(ctx context.Context, st storage.Store) error {
			writer := st.(*sqlstore.SQLStore)
			if _, err := writer.ExecContext(ctx, "UPDATE sites SET name='Snapshot after' WHERE id=?", site.ID); err != nil {
				return err
			}
			_, err := writer.ExecContext(ctx, "UPDATE site_accounts SET label='after' WHERE id=?", account.ID)
			return err
		})
	}}
	driverName := fmt.Sprintf("backup-snapshot-%d", backupSnapshotDriverSequence.Add(1))
	sql.Register(driverName, observed)
	readDB, err := sql.Open(driverName, "file:"+filepath.ToSlash(primary.path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	readDB.SetMaxOpenConns(1)
	reader := sqlstore.NewSQLStore(readDB, "sqlite")
	t.Cleanup(func() { _ = reader.Close() })
	if err := reader.ConfigureCredentialCipher(ctx, primary.cipher); err != nil {
		t.Fatal(err)
	}
	source := &Server{store: reader, siteControl: &siteControlService{store: reader, cipher: primary.cipher}}
	document, err := source.buildBackupDocument(ctx, "connections")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := primary.store.GetSite(ctx, site.ID)
	if !observed.fired.Load() || current.Name != "Snapshot after" {
		t.Fatal("the controlled concurrent edit did not commit")
	}
	if len(document.Connections.Sites) != 1 || len(document.Connections.Accounts) != 1 ||
		document.Connections.Sites[0].Name != "Snapshot before" || document.Connections.Accounts[0].Account.Label != "before" {
		t.Fatal("export mixed site and account configuration from different committed snapshots")
	}
}

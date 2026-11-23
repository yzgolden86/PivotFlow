package sql

import (
	"context"
	"database/sql"
	"errors"
)

// WithSerializableTransaction executes fn against a transaction view of the
// receiver. Every read, write and nested WithTransaction call made through the
// view uses the same *sql.Tx, so stale checks and writes are atomic with
// respect to external editors.
//
// SQLite already provides serializable transactions; other drivers request
// sql.LevelSerializable explicitly. Calling this method on a transaction view
// joins the caller's transaction instead of opening a nested one.
func (s *SQLStore) WithSerializableTransaction(
	ctx context.Context,
	fn func(context.Context, *SQLStore) error,
) error {
	if s.tx != nil {
		return fn(ctx, s)
	}

	var view *SQLStore
	err := executeSingleTransactionWithOptions(
		ctx,
		s.db,
		s.configurationTransactionOptions(),
		func(tx *sql.Tx) error {
			view = s.newTransactionView(tx)
			return fn(ctx, view)
		},
	)
	if err != nil {
		if view != nil {
			view.discardPendingChannelDeletes()
		}
		var commitErr *transactionCommitStageError
		if errors.As(err, &commitErr) {
			return &TransactionCommitUnknownError{Err: commitErr}
		}
		return err
	}
	if view != nil {
		view.applyPendingChannelDeletes()
	}
	return nil
}

func (s *SQLStore) configurationTransactionOptions() *sql.TxOptions {
	if s.IsSQLite() {
		// SQLite's default transaction mode is serializable and it rejects
		// driver-level isolation overrides.
		return nil
	}
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func (s *SQLStore) newTransactionView(tx *sql.Tx) *SQLStore {
	return &SQLStore{
		db:               s.db,
		driverName:       s.driverName,
		credentialCipher: s.credentialCipher,
		tx:               tx,
		root:             s.runtimeStore(),
	}
}

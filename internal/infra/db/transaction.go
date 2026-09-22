package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

// SQLSTATEs with which PostgreSQL reports that it aborted the transaction
// itself rather than the statement failing on its own terms.
const (
	sqlStateSerializationFailure = "40001"
	sqlStateDeadlockDetected     = "40P01"
)

// ErrTransactionAborted means PostgreSQL aborted the transaction because of a
// deadlock or a serialization failure. No work of the transaction was kept and
// nothing is wrong with the request itself, so the operation can be retried.
// It is joined to the error that failed, which stays available to errors.Is
// and errors.As. A Connect handler that runs a transaction should answer it
// with connect.CodeAborted; this template does not wire that yet because no
// RPC runs a transaction.
var ErrTransactionAborted = errors.New("transaction aborted; retry")

type transactionKey struct{}

// RunInTransaction runs fn inside one database transaction on pool. The
// transaction travels in the context handed to fn, and repositories run every
// statement through Executor, so every repository call made with that context
// (including calls on other repositories sharing the same *sqlx.DB) joins it.
// fn returning an error rolls the transaction back; otherwise it is committed.
// A call made from inside a transaction simply joins it.
func RunInTransaction(ctx context.Context, pool *sqlx.DB, fn func(context.Context) error) error {
	if _, ok := transactionFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := pool.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(context.WithValue(ctx, transactionKey{}, tx)); err != nil {
		if isTransactionAbort(err) {
			err = errors.Join(err, ErrTransactionAborted)
		}

		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
		}

		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// isTransactionAbort reports whether err carries a PostgreSQL error with which
// the server aborted the transaction, so that the caller can be told to retry
// instead of being handed an opaque failure.
func isTransactionAbort(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == sqlStateSerializationFailure || pgErr.Code == sqlStateDeadlockDetected
}

func transactionFromContext(ctx context.Context) (*sqlx.Tx, bool) {
	tx, ok := ctx.Value(transactionKey{}).(*sqlx.Tx)

	return tx, ok
}

// Executor returns the transaction carried by ctx, or pool when the call is
// not part of a transaction. Repositories use it for every statement, so that
// a call made inside a transaction opened by RunInTransaction joins it.
func Executor(ctx context.Context, pool *sqlx.DB) sqlx.ExtContext {
	if tx, ok := transactionFromContext(ctx); ok {
		return tx
	}

	return pool
}

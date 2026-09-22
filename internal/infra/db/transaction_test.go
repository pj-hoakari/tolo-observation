package db

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

func TestIsTransactionAbort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadlock", err: fmt.Errorf("record greeting: %w", &pgconn.PgError{Code: sqlStateDeadlockDetected}), want: true},
		{name: "serialization failure", err: &pgconn.PgError{Code: sqlStateSerializationFailure}, want: true},
		{name: "other PostgreSQL error", err: fmt.Errorf("record greeting: %w", &pgconn.PgError{Code: "23505"}), want: false},
		{name: "plain error", err: errors.New("abort"), want: false},
		{name: "no error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isTransactionAbort(tt.err); got != tt.want {
				t.Errorf("isTransactionAbort(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestRunInTransactionReportsAbort covers the retriable answer of a real
// transaction whose work failed with a deadlock.
func TestRunInTransactionReportsAbort(t *testing.T) {
	ctx := context.Background()
	deadlock := fmt.Errorf("record greeting: %w", &pgconn.PgError{Code: sqlStateDeadlockDetected})

	err := RunInTransaction(ctx, testDB, func(context.Context) error {
		return deadlock
	})
	if !errors.Is(err, ErrTransactionAborted) || !errors.Is(err, deadlock) {
		t.Errorf("RunInTransaction() error = %v, want it to join %v and the original error", err, ErrTransactionAborted)
	}

	// An ordinary failure is not reported as retriable.
	errAbort := errors.New("abort")

	err = RunInTransaction(ctx, testDB, func(context.Context) error {
		return errAbort
	})
	if !errors.Is(err, errAbort) || errors.Is(err, ErrTransactionAborted) {
		t.Errorf("RunInTransaction() error = %v, want %v alone", err, errAbort)
	}
}

// TestRunInTransactionJoinsSurroundingTransaction pins the nesting rule: a
// call made inside a transaction joins it instead of opening a second one, so
// the work of both commits together.
func TestRunInTransactionJoinsSurroundingTransaction(t *testing.T) {
	ctx := context.Background()

	err := RunInTransaction(ctx, testDB, func(outer context.Context) error {
		outerTx, ok := transactionFromContext(outer)
		if !ok {
			t.Fatal("outer context carries no transaction")
		}

		return RunInTransaction(outer, testDB, func(inner context.Context) error {
			innerTx, ok := transactionFromContext(inner)
			if !ok {
				t.Fatal("inner context carries no transaction")
			}

			if innerTx != outerTx {
				t.Error("nested RunInTransaction() opened a second transaction, want it to join the outer one")
			}

			if Executor(inner, testDB) != sqlx.ExtContext(innerTx) {
				t.Error("Executor() inside a transaction = pool, want the transaction")
			}

			return nil
		})
	})
	if err != nil {
		t.Fatalf("RunInTransaction() error = %v", err)
	}

	if Executor(ctx, testDB) != sqlx.ExtContext(testDB) {
		t.Error("Executor() outside a transaction != pool")
	}
}

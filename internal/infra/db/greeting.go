// Package db contains PostgreSQL-backed infrastructure implementations.
package db

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

// PostgresGreetingRepository persists issued greetings in PostgreSQL.
type PostgresGreetingRepository struct {
	db *sqlx.DB
}

// NewPostgresGreetingRepository creates a PostgreSQL-backed greeting
// repository.
func NewPostgresGreetingRepository(db *sqlx.DB) *PostgresGreetingRepository {
	return &PostgresGreetingRepository{db: db}
}

// Record stores one issued greeting under the authenticated tenant carried in
// the context. It fails closed: a context without an authenticated tenant is
// rejected, so a greeting is never recorded without a tenant owner.
func (r *PostgresGreetingRepository) Record(ctx context.Context, greeting domain.Greeting) error {
	tenantPublicID, ok := tenantctx.TenantPublicIDFromContext(ctx)
	if !ok || tenantPublicID == "" {
		return tenantctx.ErrMissing
	}

	// The statement goes through Executor, so it joins the transaction opened
	// by a surrounding RunInTransaction when there is one.
	_, err := Executor(ctx, r.db).ExecContext(ctx, `
		INSERT INTO greetings (tenant_public_id, name)
		VALUES ($1, $2)`,
		tenantPublicID, greeting.Name())

	return err
}

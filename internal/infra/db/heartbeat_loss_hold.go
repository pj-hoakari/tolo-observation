package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type PostgresHeartbeatLossHoldRepository struct {
	db *sqlx.DB
}

func NewPostgresHeartbeatLossHoldRepository(db *sqlx.DB) *PostgresHeartbeatLossHoldRepository {
	return &PostgresHeartbeatLossHoldRepository{db: db}
}

func (r *PostgresHeartbeatLossHoldRepository) CountAcrossTenants(
	ctx context.Context,
	recentSince, lostBefore time.Time,
) (domain.HeartbeatLossCount, error) {
	var row struct {
		Recent int `db:"recent"`
		Lost   int `db:"lost"`
	}

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &row, `
		SELECT count(*) AS recent, count(*) FILTER (WHERE last_heartbeat_at < $2) AS lost
		FROM edge_devices
		WHERE NOT unregistered AND last_heartbeat_at >= $1`, recentSince, lostBefore)
	if err != nil {
		return domain.HeartbeatLossCount{}, fmt.Errorf("count edge devices by heartbeat: %w", err)
	}

	return domain.HeartbeatLossCount{Recent: row.Recent, Lost: row.Lost}, nil
}

func (r *PostgresHeartbeatLossHoldRepository) FindOpen(ctx context.Context) (time.Time, bool, error) {
	var startedAt time.Time

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &startedAt, `
		SELECT started_at FROM heartbeat_loss_holds WHERE ended_at IS NULL`)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}

	if err != nil {
		return time.Time{}, false, fmt.Errorf("select open heartbeat loss hold: %w", err)
	}

	return startedAt, true, nil
}

func (r *PostgresHeartbeatLossHoldRepository) Start(ctx context.Context, startedAt time.Time, deviceCount int) (bool, error) {
	result, err := Executor(ctx, r.db).ExecContext(ctx, `
		INSERT INTO heartbeat_loss_holds (started_at, device_count)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, startedAt, deviceCount)
	if err != nil {
		return false, fmt.Errorf("insert heartbeat loss hold: %w", err)
	}

	return affectedOne(result)
}

func (r *PostgresHeartbeatLossHoldRepository) End(ctx context.Context, startedAt, endedAt time.Time) (bool, error) {
	result, err := Executor(ctx, r.db).ExecContext(ctx, `
		UPDATE heartbeat_loss_holds SET ended_at = GREATEST($2, started_at)
		WHERE ended_at IS NULL AND started_at = $1`, startedAt, endedAt)
	if err != nil {
		return false, fmt.Errorf("end heartbeat loss hold: %w", err)
	}

	return affectedOne(result)
}

func affectedOne(result sql.Result) (bool, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count affected heartbeat loss holds: %w", err)
	}

	return affected == 1, nil
}

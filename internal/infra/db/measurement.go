package db

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type PostgresMeasurementRepository struct {
	db *sqlx.DB
}

func NewPostgresMeasurementRepository(db *sqlx.DB) *PostgresMeasurementRepository {
	return &PostgresMeasurementRepository{db: db}
}

func (r *PostgresMeasurementRepository) RecordAll(ctx context.Context, tenantPublicID, eventID string, measurements []domain.Measurement) error {
	if len(measurements) == 0 {
		return nil
	}

	return RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		executor := Executor(ctx, r.db)

		for _, measurement := range measurements {
			_, err := executor.ExecContext(ctx, `
				INSERT INTO measurements (
					tenant_public_id, event_id, observation_point_id,
					window_start, window_end, count_in, count_out, rate_in, rate_out)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				tenantPublicID, eventID, string(measurement.ObservationPointID),
				measurement.WindowStart, measurement.WindowEnd,
				measurement.CountIn, measurement.CountOut,
				measurement.RateIn(), measurement.RateOut())
			if err != nil {
				return fmt.Errorf("insert measurement: %w", err)
			}
		}

		return nil
	})
}

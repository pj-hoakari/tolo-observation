package db

import (
	"context"
	"fmt"
	"time"

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

type measurementWindowRow struct {
	ObservationPointID string    `db:"observation_point_id"`
	WindowStart        time.Time `db:"window_start"`
	WindowEnd          time.Time `db:"window_end"`
	CountIn            int32     `db:"count_in"`
	CountOut           int32     `db:"count_out"`
}

func (r *PostgresMeasurementRepository) ListWindowEndingAfter(
	ctx context.Context,
	eventID string,
	after time.Time,
) ([]domain.Measurement, error) {
	var rows []measurementWindowRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &rows, `
		SELECT observation_point_id, window_start, window_end, count_in, count_out
		FROM measurements
		WHERE event_id = $1 AND window_end > $2
		ORDER BY window_end, id`, eventID, after)
	if err != nil {
		return nil, fmt.Errorf("select measurements: %w", err)
	}

	measurements := make([]domain.Measurement, 0, len(rows))
	for _, row := range rows {
		measurements = append(measurements, domain.Measurement{
			ObservationPointID: domain.ObservationPointID(row.ObservationPointID),
			WindowStart:        row.WindowStart,
			WindowEnd:          row.WindowEnd,
			CountIn:            row.CountIn,
			CountOut:           row.CountOut,
		})
	}

	return measurements, nil
}

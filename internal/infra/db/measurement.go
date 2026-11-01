package db

import (
	"context"
	"fmt"
	"log/slog"
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

func (r *PostgresMeasurementRepository) RecordAll(
	ctx context.Context,
	tenantPublicID, eventID string,
	edgeDeviceID domain.EdgeDeviceID,
	measurements []domain.Measurement,
) (int32, error) {
	if len(measurements) == 0 {
		return 0, nil
	}

	var accepted int32

	err := RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		executor := Executor(ctx, r.db)

		for _, measurement := range measurements {
			result, err := executor.ExecContext(ctx, `
				INSERT INTO measurements (
					tenant_public_id, event_id, edge_device_id, observation_point_id,
					window_start, window_end, count_in, count_out, rate_in, rate_out, mean_detected_people)
				VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11)
				ON CONFLICT (tenant_public_id, event_id, edge_device_id, observation_point_id, window_start)
					WHERE edge_device_id IS NOT NULL
				DO NOTHING`,
				tenantPublicID, eventID, string(edgeDeviceID), string(measurement.ObservationPointID),
				measurement.WindowStart, measurement.WindowEnd,
				measurement.CountIn, measurement.CountOut,
				measurement.RateIn(), measurement.RateOut(), measurement.MeanDetectedPeople)
			if err != nil {
				return fmt.Errorf("insert measurement: %w", err)
			}

			inserted, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("insert measurement: %w", err)
			}

			if inserted == 1 {
				accepted++

				continue
			}

			if err := logDivergentResend(ctx, executor, tenantPublicID, eventID, edgeDeviceID, measurement); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return accepted, nil
}

func logDivergentResend(
	ctx context.Context,
	executor sqlx.ExtContext,
	tenantPublicID, eventID string,
	edgeDeviceID domain.EdgeDeviceID,
	resent domain.Measurement,
) error {
	var stored measurementWindowRow

	err := sqlx.GetContext(ctx, executor, &stored, `
		SELECT observation_point_id, window_start, window_end, count_in, count_out, mean_detected_people
		FROM measurements
		WHERE tenant_public_id = $1 AND event_id = $2 AND edge_device_id = $3
			AND observation_point_id = $4 AND window_start = $5`,
		tenantPublicID, eventID, string(edgeDeviceID), string(resent.ObservationPointID), resent.WindowStart)
	if err != nil {
		return fmt.Errorf("select accepted measurement: %w", err)
	}

	if stored.WindowEnd.Equal(resent.WindowEnd.Round(time.Microsecond)) && stored.CountIn == resent.CountIn && stored.CountOut == resent.CountOut &&
		equalOptional(stored.MeanDetectedPeople, resent.MeanDetectedPeople) {
		return nil
	}

	slog.WarnContext(ctx, "resent measurement differs from the accepted one",
		"event_id", eventID,
		"edge_device_id", string(edgeDeviceID),
		"observation_point_id", string(resent.ObservationPointID),
		"window_start", resent.WindowStart,
		"accepted_window_end", stored.WindowEnd, "resent_window_end", resent.WindowEnd,
		"accepted_count_in", stored.CountIn, "resent_count_in", resent.CountIn,
		"accepted_count_out", stored.CountOut, "resent_count_out", resent.CountOut,
		"accepted_mean_detected_people", stored.MeanDetectedPeople, "resent_mean_detected_people", resent.MeanDetectedPeople,
	)

	return nil
}

func equalOptional(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

type measurementWindowRow struct {
	ObservationPointID string    `db:"observation_point_id"`
	WindowStart        time.Time `db:"window_start"`
	WindowEnd          time.Time `db:"window_end"`
	CountIn            int32     `db:"count_in"`
	CountOut           int32     `db:"count_out"`
	MeanDetectedPeople *float64  `db:"mean_detected_people"`
}

func (r *PostgresMeasurementRepository) ListWindowEndingSince(
	ctx context.Context,
	eventID string,
	since time.Time,
) ([]domain.Measurement, error) {
	var rows []measurementWindowRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &rows, `
		SELECT observation_point_id, window_start, window_end, count_in, count_out, mean_detected_people
		FROM measurements
		WHERE event_id = $1 AND window_end >= $2
		ORDER BY window_end, id`, eventID, since)
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
			MeanDetectedPeople: row.MeanDetectedPeople,
		})
	}

	return measurements, nil
}

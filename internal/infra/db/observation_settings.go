package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

var eventTypeValues = map[domain.EventType]string{
	domain.EventTypeShortTerm: "short_term",
	domain.EventTypeLongTerm:  "long_term",
}

type PostgresObservationSettingsRepository struct {
	db *sqlx.DB
}

func NewPostgresObservationSettingsRepository(db *sqlx.DB) *PostgresObservationSettingsRepository {
	return &PostgresObservationSettingsRepository{db: db}
}

type observationSettingsRow struct {
	TenantPublicID    string `db:"tenant_public_id"`
	HistoryWindowDays int32  `db:"history_window_days"`
	EventType         string `db:"event_type"`
}

func (r *PostgresObservationSettingsRepository) Find(
	ctx context.Context,
	eventID string,
) (domain.ObservationSettings, error) {
	var row observationSettingsRow

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &row, `
		SELECT tenant_public_id, history_window_days, event_type
		FROM observation_settings
		WHERE event_id = $1`, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ObservationSettings{}, repository.ErrObservationSettingsNotFound
	}

	if err != nil {
		return domain.ObservationSettings{}, fmt.Errorf("select observation settings: %w", err)
	}

	if err := tenantctx.VerifyOwnership(ctx, row.TenantPublicID); err != nil {
		return domain.ObservationSettings{}, err
	}

	for eventType, value := range eventTypeValues {
		if value == row.EventType {
			return domain.ObservationSettings{HistoryWindowDays: row.HistoryWindowDays, EventType: eventType}, nil
		}
	}

	return domain.ObservationSettings{}, fmt.Errorf("unknown stored event type %q", row.EventType)
}

func (r *PostgresObservationSettingsRepository) Save(
	ctx context.Context,
	tenantPublicID, eventID string,
	settings domain.ObservationSettings,
) error {
	result, err := Executor(ctx, r.db).ExecContext(ctx, `
		INSERT INTO observation_settings (event_id, tenant_public_id, history_window_days, event_type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO UPDATE
		SET history_window_days = EXCLUDED.history_window_days, event_type = EXCLUDED.event_type, updated_at = now()
		WHERE observation_settings.tenant_public_id = EXCLUDED.tenant_public_id`,
		eventID, tenantPublicID, settings.HistoryWindowDays, eventTypeValues[settings.EventType])
	if err != nil {
		return fmt.Errorf("upsert observation settings: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count upserted observation settings: %w", err)
	}

	if affected == 0 {
		return tenantctx.ErrMismatch
	}

	return nil
}

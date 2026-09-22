package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

type PostgresEdgeDeviceRepository struct {
	db *sqlx.DB
}

func NewPostgresEdgeDeviceRepository(db *sqlx.DB) *PostgresEdgeDeviceRepository {
	return &PostgresEdgeDeviceRepository{db: db}
}

type edgeDeviceRow struct {
	InternalID      int64      `db:"id"`
	ID              string     `db:"edge_device_id"`
	TenantPublicID  string     `db:"tenant_public_id"`
	EventID         string     `db:"event_id"`
	Name            string     `db:"name"`
	Unregistered    bool       `db:"unregistered"`
	LastHeartbeatAt *time.Time `db:"last_heartbeat_at"`
}

func (r edgeDeviceRow) toDomain(observationPoints []domain.ObservationPoint) domain.EdgeDevice {
	if observationPoints == nil {
		observationPoints = []domain.ObservationPoint{}
	}

	return domain.EdgeDevice{
		ID:                domain.EdgeDeviceID(r.ID),
		TenantPublicID:    r.TenantPublicID,
		EventID:           r.EventID,
		Name:              r.Name,
		Unregistered:      r.Unregistered,
		LastHeartbeatAt:   r.LastHeartbeatAt,
		ObservationPoints: observationPoints,
	}
}

type observationPointRow struct {
	EdgeDeviceInternalID int64      `db:"edge_device_id"`
	ID                   string     `db:"observation_point_id"`
	Name                 string     `db:"name"`
	Enabled              bool       `db:"enabled"`
	LastActiveAt         *time.Time `db:"last_active_at"`
}

const selectEdgeDeviceColumns = `
	d.id, d.edge_device_id, d.tenant_public_id, d.event_id, d.name,
	d.unregistered, d.last_heartbeat_at`

func (r *PostgresEdgeDeviceRepository) Create(ctx context.Context, device domain.EdgeDevice) error {
	return RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		executor := Executor(ctx, r.db)

		var internalID int64

		err := sqlx.GetContext(ctx, executor, &internalID, `
			INSERT INTO edge_devices (edge_device_id, tenant_public_id, event_id, name, unregistered, last_heartbeat_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`,
			string(device.ID), device.TenantPublicID, device.EventID, device.Name,
			device.Unregistered, device.LastHeartbeatAt)
		if err != nil {
			return fmt.Errorf("insert edge device: %w", err)
		}

		for _, point := range device.ObservationPoints {
			_, err := executor.ExecContext(ctx, `
				INSERT INTO observation_points (observation_point_id, edge_device_id, name, enabled, last_active_at)
				VALUES ($1, $2, $3, $4, $5)`,
				string(point.ID), internalID, point.Name, point.Enabled, point.LastActiveAt)
			if err != nil {
				return fmt.Errorf("insert observation point: %w", err)
			}
		}

		return nil
	})
}

func (r *PostgresEdgeDeviceRepository) FindByID(ctx context.Context, id domain.EdgeDeviceID) (domain.EdgeDevice, error) {
	return r.findOne(ctx, `
		SELECT`+selectEdgeDeviceColumns+`
		FROM edge_devices d
		WHERE d.edge_device_id = $1`, string(id))
}

func (r *PostgresEdgeDeviceRepository) FindByObservationPointID(ctx context.Context, id domain.ObservationPointID) (domain.EdgeDevice, error) {
	return r.findOne(ctx, `
		SELECT`+selectEdgeDeviceColumns+`
		FROM edge_devices d
		JOIN observation_points p ON p.edge_device_id = d.id
		WHERE p.observation_point_id = $1`, string(id))
}

func (r *PostgresEdgeDeviceRepository) ListByEvent(ctx context.Context, eventID string, includeUnregistered bool) ([]domain.EdgeDevice, error) {
	var rows []edgeDeviceRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &rows, `
		SELECT`+selectEdgeDeviceColumns+`
		FROM edge_devices d
		WHERE d.event_id = $1 AND ($2 OR NOT d.unregistered)
		ORDER BY d.id`, eventID, includeUnregistered)
	if err != nil {
		return nil, fmt.Errorf("select edge devices: %w", err)
	}

	internalIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		internalIDs = append(internalIDs, row.InternalID)
	}

	observationPoints, err := r.observationPointsByDevice(ctx, internalIDs)
	if err != nil {
		return nil, err
	}

	devices := make([]domain.EdgeDevice, 0, len(rows))

	for _, row := range rows {
		device := row.toDomain(observationPoints[row.InternalID])
		if err := tenantctx.VerifyOwnership(ctx, device.TenantPublicID); err != nil {
			return nil, err
		}

		devices = append(devices, device)
	}

	return devices, nil
}

func (r *PostgresEdgeDeviceRepository) Save(ctx context.Context, device domain.EdgeDevice) error {
	return RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		executor := Executor(ctx, r.db)

		var internalID int64

		err := sqlx.GetContext(ctx, executor, &internalID, `
			UPDATE edge_devices
			SET name = $2, unregistered = $3, last_heartbeat_at = $4
			WHERE edge_device_id = $1
			RETURNING id`,
			string(device.ID), device.Name, device.Unregistered, device.LastHeartbeatAt)

		if errors.Is(err, sql.ErrNoRows) {
			return repository.ErrEdgeDeviceNotFound
		}

		if err != nil {
			return fmt.Errorf("update edge device: %w", err)
		}

		for _, point := range device.ObservationPoints {
			_, err := executor.ExecContext(ctx, `
				UPDATE observation_points
				SET name = $3, enabled = $4, last_active_at = $5
				WHERE observation_point_id = $1 AND edge_device_id = $2`,
				string(point.ID), internalID, point.Name, point.Enabled, point.LastActiveAt)
			if err != nil {
				return fmt.Errorf("update observation point: %w", err)
			}
		}

		return nil
	})
}

func (r *PostgresEdgeDeviceRepository) findOne(ctx context.Context, query string, arg any) (domain.EdgeDevice, error) {
	var row edgeDeviceRow

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &row, query, arg)

	if errors.Is(err, sql.ErrNoRows) {
		return domain.EdgeDevice{}, repository.ErrEdgeDeviceNotFound
	}

	if err != nil {
		return domain.EdgeDevice{}, fmt.Errorf("select edge device: %w", err)
	}

	observationPoints, err := r.observationPointsByDevice(ctx, []int64{row.InternalID})
	if err != nil {
		return domain.EdgeDevice{}, err
	}

	device := row.toDomain(observationPoints[row.InternalID])
	if err := tenantctx.VerifyOwnership(ctx, device.TenantPublicID); err != nil {
		return domain.EdgeDevice{}, err
	}

	return device, nil
}

func (r *PostgresEdgeDeviceRepository) observationPointsByDevice(ctx context.Context, internalIDs []int64) (map[int64][]domain.ObservationPoint, error) {
	byDevice := make(map[int64][]domain.ObservationPoint, len(internalIDs))
	if len(internalIDs) == 0 {
		return byDevice, nil
	}

	var rows []observationPointRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &rows, `
		SELECT edge_device_id, observation_point_id, name, enabled, last_active_at
		FROM observation_points
		WHERE edge_device_id = ANY($1)
		ORDER BY id`, internalIDs)
	if err != nil {
		return nil, fmt.Errorf("select observation points: %w", err)
	}

	for _, row := range rows {
		byDevice[row.EdgeDeviceInternalID] = append(byDevice[row.EdgeDeviceInternalID], domain.ObservationPoint{
			ID:           domain.ObservationPointID(row.ID),
			Name:         row.Name,
			Enabled:      row.Enabled,
			LastActiveAt: row.LastActiveAt,
		})
	}

	return byDevice, nil
}

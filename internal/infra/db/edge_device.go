package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

const sqlStateUniqueViolation = "23505"

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
	Kind            string     `db:"kind"`
}

func (r edgeDeviceRow) toDomain(
	observationPoints []domain.ObservationPoint,
	cameraLinks []domain.CameraLink,
) domain.EdgeDevice {
	if observationPoints == nil {
		observationPoints = []domain.ObservationPoint{}
	}

	if cameraLinks == nil {
		cameraLinks = []domain.CameraLink{}
	}

	kind, err := domain.ParseEdgeDeviceKind(r.Kind)
	if err != nil {
		kind = domain.EdgeDeviceKindObservationPage
	}

	return domain.EdgeDevice{
		ID:                domain.EdgeDeviceID(r.ID),
		TenantPublicID:    r.TenantPublicID,
		EventID:           r.EventID,
		Name:              r.Name,
		Unregistered:      r.Unregistered,
		LastHeartbeatAt:   r.LastHeartbeatAt,
		Kind:              kind,
		ObservationPoints: observationPoints,
		CameraLinks:       cameraLinks,
	}
}

type observationPointRow struct {
	EdgeDeviceInternalID int64      `db:"edge_device_id"`
	ID                   string     `db:"observation_point_id"`
	Name                 string     `db:"name"`
	Enabled              bool       `db:"enabled"`
	LastActiveAt         *time.Time `db:"last_active_at"`
}

type cameraLinkRow struct {
	OwnerInternalID    int64  `db:"owner_id"`
	CameraEdgeDeviceID string `db:"camera_edge_device_id"`
	ObservationPointID string `db:"observation_point_id"`
}

const selectEdgeDeviceColumns = `
	d.id, d.edge_device_id, d.tenant_public_id, d.event_id, d.name,
	d.unregistered, d.last_heartbeat_at, d.kind`

func (r *PostgresEdgeDeviceRepository) Create(ctx context.Context, device domain.EdgeDevice) error {
	return RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		executor := Executor(ctx, r.db)

		var internalID int64

		err := sqlx.GetContext(ctx, executor, &internalID, `
			INSERT INTO edge_devices (edge_device_id, tenant_public_id, event_id, name, unregistered, last_heartbeat_at, kind)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id`,
			string(device.ID), device.TenantPublicID, device.EventID, device.Name,
			device.Unregistered, device.LastHeartbeatAt, device.Kind.String())
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

		if err := insertCameraLinks(ctx, executor, internalID, device.CameraLinks); err != nil {
			return err
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

	cameraLinks, err := r.cameraLinksByDevice(ctx, internalIDs)
	if err != nil {
		return nil, err
	}

	devices := make([]domain.EdgeDevice, 0, len(rows))

	for _, row := range rows {
		device := row.toDomain(observationPoints[row.InternalID], cameraLinks[row.InternalID])
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
			SET name = $2, unregistered = $3, last_heartbeat_at = $4, kind = $5
			WHERE edge_device_id = $1
			RETURNING id`,
			string(device.ID), device.Name, device.Unregistered, device.LastHeartbeatAt, device.Kind.String())

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

		if device.Kind == domain.EdgeDeviceKindCamera {
			if _, err := executor.ExecContext(ctx, `
				DELETE FROM camera_links WHERE camera_edge_device_id = $1`, internalID); err != nil {
				return fmt.Errorf("delete camera links: %w", err)
			}

			if err := insertCameraLinks(ctx, executor, internalID, device.CameraLinks); err != nil {
				return err
			}
		}

		return nil
	})
}

func (r *PostgresEdgeDeviceRepository) SaveHeartbeat(ctx context.Context, device domain.EdgeDevice) error {
	return RunInTransaction(ctx, r.db, func(ctx context.Context) error {
		executor := Executor(ctx, r.db)

		var internalID int64

		err := sqlx.GetContext(ctx, executor, &internalID, `
			UPDATE edge_devices
			SET last_heartbeat_at = $2
			WHERE edge_device_id = $1 AND NOT unregistered
			RETURNING id`,
			string(device.ID), device.LastHeartbeatAt)

		if errors.Is(err, sql.ErrNoRows) {
			return repository.ErrEdgeDeviceNotFound
		}

		if err != nil {
			return fmt.Errorf("update edge device heartbeat: %w", err)
		}

		for _, point := range device.ObservationPoints {
			if point.LastActiveAt == nil {
				continue
			}

			_, err := executor.ExecContext(ctx, `
				UPDATE observation_points
				SET last_active_at = $3
				WHERE observation_point_id = $1 AND edge_device_id = $2`,
				string(point.ID), internalID, point.LastActiveAt)
			if err != nil {
				return fmt.Errorf("update observation point activity: %w", err)
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

	cameraLinks, err := r.cameraLinksByDevice(ctx, []int64{row.InternalID})
	if err != nil {
		return domain.EdgeDevice{}, err
	}

	device := row.toDomain(observationPoints[row.InternalID], cameraLinks[row.InternalID])
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

func (r *PostgresEdgeDeviceRepository) cameraLinksByDevice(
	ctx context.Context,
	internalIDs []int64,
) (map[int64][]domain.CameraLink, error) {
	byDevice := make(map[int64][]domain.CameraLink, len(internalIDs))
	if len(internalIDs) == 0 {
		return byDevice, nil
	}

	var cameraRows []cameraLinkRow

	err := sqlx.SelectContext(ctx, Executor(ctx, r.db), &cameraRows, `
		SELECT cl.camera_edge_device_id AS owner_id,
			c.edge_device_id AS camera_edge_device_id,
			p.observation_point_id AS observation_point_id
		FROM camera_links cl
		JOIN edge_devices c ON c.id = cl.camera_edge_device_id
		JOIN observation_points p ON p.id = cl.observation_point_id
		WHERE cl.camera_edge_device_id = ANY($1)
		ORDER BY cl.id`, internalIDs)
	if err != nil {
		return nil, fmt.Errorf("select camera links of cameras: %w", err)
	}

	var pageRows []cameraLinkRow

	err = sqlx.SelectContext(ctx, Executor(ctx, r.db), &pageRows, `
		SELECT p.edge_device_id AS owner_id,
			c.edge_device_id AS camera_edge_device_id,
			p.observation_point_id AS observation_point_id
		FROM camera_links cl
		JOIN edge_devices c ON c.id = cl.camera_edge_device_id
		JOIN observation_points p ON p.id = cl.observation_point_id
		WHERE p.edge_device_id = ANY($1) AND c.kind = 'camera' AND NOT c.unregistered
		ORDER BY cl.id`, internalIDs)
	if err != nil {
		return nil, fmt.Errorf("select camera links of observation pages: %w", err)
	}

	for _, row := range append(cameraRows, pageRows...) {
		byDevice[row.OwnerInternalID] = append(byDevice[row.OwnerInternalID], domain.CameraLink{
			CameraEdgeDeviceID: domain.EdgeDeviceID(row.CameraEdgeDeviceID),
			ObservationPointID: domain.ObservationPointID(row.ObservationPointID),
		})
	}

	return byDevice, nil
}

func insertCameraLinks(
	ctx context.Context,
	executor sqlx.ExtContext,
	cameraInternalID int64,
	links []domain.CameraLink,
) error {
	for _, link := range links {
		var pointInternalID int64

		err := sqlx.GetContext(ctx, executor, &pointInternalID, `
			SELECT id FROM observation_points WHERE observation_point_id = $1`,
			string(link.ObservationPointID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrObservationPointNotFound
		}

		if err != nil {
			return fmt.Errorf("select linked observation point: %w", err)
		}

		_, err = executor.ExecContext(ctx, `
			INSERT INTO camera_links (camera_edge_device_id, observation_point_id)
			VALUES ($1, $2)`, cameraInternalID, pointInternalID)
		if err != nil {
			if isUniqueViolation(err) {
				return domain.ErrObservationPointAlreadyLinked
			}

			return fmt.Errorf("insert camera link: %w", err)
		}
	}

	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == sqlStateUniqueViolation
}

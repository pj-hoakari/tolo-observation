package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

type PostgresGuestStatusRepository struct {
	db *sqlx.DB
}

func NewPostgresGuestStatusRepository(db *sqlx.DB) *PostgresGuestStatusRepository {
	return &PostgresGuestStatusRepository{db: db}
}

type crowdStatusJSON struct {
	PointID string `json:"point_id,omitempty"`
	RouteID string `json:"route_id,omitempty"`
	Level   int    `json:"level"`
	Danger  bool   `json:"danger"`
}

type queueStatusJSON struct {
	QueueID                 string   `json:"queue_id"`
	GatePointIDs            []string `json:"gate_point_ids"`
	WaitTimeMinutes         float64  `json:"wait_time_minutes"`
	TurnaroundCount         int32    `json:"turnaround_count"`
	RecommendedGatePointIDs []string `json:"recommended_gate_point_ids"`
}

type guestStatusRow struct {
	EventID  string    `db:"event_id"`
	Sequence uint64    `db:"sequence"`
	TakenAt  time.Time `db:"taken_at"`
	Crowd    []byte    `db:"crowd"`
	Queues   []byte    `db:"queues"`
}

func (r *PostgresGuestStatusRepository) SaveGuestStatus(
	ctx context.Context,
	tenantPublicID string,
	status domain.GuestStatus,
) (domain.GuestStatus, error) {
	crowd := make([]crowdStatusJSON, 0, len(status.Crowd))
	for _, c := range status.Crowd {
		crowd = append(crowd, crowdStatusJSON{
			PointID: c.Anchor.PointID,
			RouteID: c.Anchor.RouteID,
			Level:   int(c.Level),
			Danger:  c.Danger,
		})
	}

	queues := make([]queueStatusJSON, 0, len(status.Queues))
	for _, q := range status.Queues {
		queues = append(queues, queueStatusJSON(q))
	}

	encodedCrowd, err := json.Marshal(crowd)
	if err != nil {
		return domain.GuestStatus{}, fmt.Errorf("encode guest crowd status: %w", err)
	}

	encodedQueues, err := json.Marshal(queues)
	if err != nil {
		return domain.GuestStatus{}, fmt.Errorf("encode guest queue status: %w", err)
	}

	err = sqlx.GetContext(ctx, Executor(ctx, r.db), &status.Sequence, `
		INSERT INTO guest_statuses AS g (event_id, tenant_public_id, sequence, taken_at, crowd, queues)
		VALUES ($1, $2, 1, $3, $4, $5)
		ON CONFLICT (event_id) DO UPDATE
		SET sequence = g.sequence + 1,
			taken_at = EXCLUDED.taken_at,
			crowd = EXCLUDED.crowd,
			queues = EXCLUDED.queues
		RETURNING sequence`,
		status.EventID, tenantPublicID, status.TakenAt, encodedCrowd, encodedQueues)
	if err != nil {
		return domain.GuestStatus{}, fmt.Errorf("upsert guest status: %w", err)
	}

	return status, nil
}

func (r *PostgresGuestStatusRepository) LatestGuestStatus(ctx context.Context, eventID string) (domain.GuestStatus, error) {
	var row guestStatusRow

	err := sqlx.GetContext(ctx, Executor(ctx, r.db), &row, `
		SELECT event_id, sequence, taken_at, crowd, queues
		FROM guest_statuses
		WHERE event_id = $1`, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.GuestStatus{}, repository.ErrGuestStatusNotFound
	}

	if err != nil {
		return domain.GuestStatus{}, fmt.Errorf("select guest status: %w", err)
	}

	var (
		crowd  []crowdStatusJSON
		queues []queueStatusJSON
	)

	if err := json.Unmarshal(row.Crowd, &crowd); err != nil {
		return domain.GuestStatus{}, fmt.Errorf("decode guest crowd status: %w", err)
	}

	if err := json.Unmarshal(row.Queues, &queues); err != nil {
		return domain.GuestStatus{}, fmt.Errorf("decode guest queue status: %w", err)
	}

	status := domain.GuestStatus{
		EventID:  row.EventID,
		Sequence: row.Sequence,
		TakenAt:  row.TakenAt,
		Crowd:    make([]domain.CrowdStatus, 0, len(crowd)),
		Queues:   make([]domain.QueueStatus, 0, len(queues)),
	}

	for _, c := range crowd {
		status.Crowd = append(status.Crowd, domain.CrowdStatus{
			Anchor: domain.GraphAnchor{PointID: c.PointID, RouteID: c.RouteID},
			Level:  domain.CrowdLevel(c.Level),
			Danger: c.Danger,
		})
	}

	for _, q := range queues {
		status.Queues = append(status.Queues, domain.QueueStatus(q))
	}

	return status, nil
}

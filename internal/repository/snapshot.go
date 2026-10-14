package repository

import (
	"context"
	"errors"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

var (
	ErrSnapshotNotFound           = errors.New("snapshot not found")
	ErrOptimizationOutcomeMissing = errors.New("optimization outcome not found")
)

type SnapshotRepository interface {
	SaveSnapshot(ctx context.Context, tenantPublicID string, snapshot domain.Snapshot) error
	LatestSnapshot(ctx context.Context, eventID string) (domain.Snapshot, error)
	ListSnapshotsEndingAfter(ctx context.Context, eventID string, after time.Time) ([]domain.Snapshot, error)
	SummarizeArcHistory(ctx context.Context, eventID string, since time.Time) ([]domain.ArcHistory, error)
	EarliestSnapshotStart(ctx context.Context, eventID string) (time.Time, error)
	ListSensorSets(ctx context.Context, eventID string) ([]domain.SensorSet, error)
	SaveSensorSets(ctx context.Context, eventID string, sets []domain.SensorSet) error
	SaveOptimizationOutcome(ctx context.Context, eventID string, outcome domain.OptimizationOutcome) error
	LatestOptimizationOutcome(ctx context.Context, eventID string) (domain.OptimizationOutcome, error)
}

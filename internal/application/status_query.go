package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

type EventOverview struct {
	Snapshot *domain.Snapshot
}

type StatusQueryUseCases interface {
	GetEventOverview(ctx context.Context, eventID string) (EventOverview, error)
}

type StatusQueryService struct {
	snapshots repository.SnapshotRepository
}

func NewStatusQueryService(snapshots repository.SnapshotRepository) *StatusQueryService {
	return &StatusQueryService{snapshots: snapshots}
}

func (s *StatusQueryService) GetEventOverview(ctx context.Context, eventID string) (EventOverview, error) {
	if err := tenantctx.EnsureEvent(ctx, eventID); err != nil {
		return EventOverview{}, err
	}

	snapshot, err := s.snapshots.LatestSnapshot(ctx, eventID)
	if errors.Is(err, repository.ErrSnapshotNotFound) {
		return EventOverview{Snapshot: nil}, nil
	}

	if err != nil {
		return EventOverview{}, fmt.Errorf("find latest snapshot: %w", err)
	}

	return EventOverview{Snapshot: &snapshot}, nil
}

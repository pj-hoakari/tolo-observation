package repository

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

var ErrEdgeDeviceNotFound = errors.New("edge device not found")

type EdgeDeviceRepository interface {
	Create(ctx context.Context, device domain.EdgeDevice) error
	FindByID(ctx context.Context, id domain.EdgeDeviceID) (domain.EdgeDevice, error)
	FindByObservationPointID(ctx context.Context, id domain.ObservationPointID) (domain.EdgeDevice, error)
	ListByEvent(ctx context.Context, eventID string, includeUnregistered bool) ([]domain.EdgeDevice, error)
	Save(ctx context.Context, device domain.EdgeDevice) error
	SaveHeartbeat(ctx context.Context, device domain.EdgeDevice) error
}

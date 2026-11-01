package repository

import (
	"context"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type MeasurementRepository interface {
	RecordAll(
		ctx context.Context,
		tenantPublicID, eventID string,
		edgeDeviceID domain.EdgeDeviceID,
		measurements []domain.Measurement,
	) (int32, error)
	ListWindowEndingSince(ctx context.Context, eventID string, since time.Time) ([]domain.Measurement, error)
}

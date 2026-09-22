package repository

import (
	"context"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type MeasurementRepository interface {
	RecordAll(ctx context.Context, tenantPublicID, eventID string, measurements []domain.Measurement) error
}

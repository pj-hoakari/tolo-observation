package repository

import (
	"context"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type MeasurementRepository interface {
	RecordAll(ctx context.Context, tenantPublicID, eventID string, measurements []domain.Measurement) error
	ListWindowEndingAfter(ctx context.Context, eventID string, after time.Time) ([]domain.Measurement, error)
}

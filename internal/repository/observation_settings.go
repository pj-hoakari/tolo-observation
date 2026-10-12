package repository

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

var ErrObservationSettingsNotFound = errors.New("observation settings not found")

type ObservationSettingsRepository interface {
	Find(ctx context.Context, eventID string) (domain.ObservationSettings, error)
	Save(ctx context.Context, tenantPublicID, eventID string, settings domain.ObservationSettings) error
}

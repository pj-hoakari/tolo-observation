package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

type UpdateObservationSettingsInput struct {
	EventID           string
	HistoryWindowDays int32
	EventType         domain.EventType
}

type ObservationSettingsUseCases interface {
	GetObservationSettings(ctx context.Context, eventID string) (domain.ObservationSettings, error)
	UpdateObservationSettings(ctx context.Context, input UpdateObservationSettingsInput) (domain.ObservationSettings, error)
}

type ObservationSettingsService struct {
	settings repository.ObservationSettingsRepository
}

func NewObservationSettingsService(settings repository.ObservationSettingsRepository) *ObservationSettingsService {
	return &ObservationSettingsService{settings: settings}
}

func (s *ObservationSettingsService) GetObservationSettings(
	ctx context.Context,
	eventID string,
) (domain.ObservationSettings, error) {
	if err := tenantctx.EnsureEvent(ctx, eventID); err != nil {
		return domain.ObservationSettings{}, err
	}

	return findObservationSettings(ctx, s.settings, eventID)
}

func (s *ObservationSettingsService) UpdateObservationSettings(
	ctx context.Context,
	input UpdateObservationSettingsInput,
) (domain.ObservationSettings, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return domain.ObservationSettings{}, err
	}

	tenantPublicID, ok := tenantctx.TenantPublicIDFromContext(ctx)
	if !ok {
		return domain.ObservationSettings{}, tenantctx.ErrMissing
	}

	settings, err := domain.NewObservationSettings(input.HistoryWindowDays, input.EventType)
	if err != nil {
		return domain.ObservationSettings{}, err
	}

	if err := s.settings.Save(ctx, tenantPublicID, input.EventID, settings); err != nil {
		return domain.ObservationSettings{}, fmt.Errorf("save observation settings: %w", err)
	}

	return settings, nil
}

func findObservationSettings(
	ctx context.Context,
	settings repository.ObservationSettingsRepository,
	eventID string,
) (domain.ObservationSettings, error) {
	found, err := settings.Find(ctx, eventID)
	if errors.Is(err, repository.ErrObservationSettingsNotFound) {
		return domain.DefaultObservationSettings(), nil
	}

	if err != nil {
		return domain.ObservationSettings{}, fmt.Errorf("find observation settings: %w", err)
	}

	return found, nil
}

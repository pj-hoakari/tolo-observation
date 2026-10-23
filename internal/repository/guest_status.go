package repository

import (
	"context"
	"errors"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

var ErrGuestStatusNotFound = errors.New("guest status not found")

type GuestStatusRepository interface {
	SaveGuestStatus(ctx context.Context, tenantPublicID string, status domain.GuestStatus) (domain.GuestStatus, error)
	LatestGuestStatus(ctx context.Context, eventID string) (domain.GuestStatus, error)
}

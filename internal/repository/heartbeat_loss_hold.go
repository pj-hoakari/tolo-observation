package repository

import (
	"context"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type HeartbeatLossHoldRepository interface {
	CountAcrossTenants(ctx context.Context, recentSince, lostBefore time.Time) (domain.HeartbeatLossCount, error)
	FindOpen(ctx context.Context) (startedAt time.Time, open bool, err error)
	Start(ctx context.Context, startedAt time.Time, deviceCount int) (bool, error)
	End(ctx context.Context, startedAt, endedAt time.Time) (bool, error)
}

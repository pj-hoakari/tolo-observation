package application

//go:generate go tool mockgen -source=../repository/heartbeat_loss_hold.go -destination=mock_heartbeat_loss_hold_repository_test.go -package=application_test

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

type HeartbeatLossHolds struct {
	holds  repository.HeartbeatLossHoldRepository
	policy domain.HeartbeatLossHoldPolicy
}

func NewHeartbeatLossHolds(holds repository.HeartbeatLossHoldRepository, policy domain.HeartbeatLossHoldPolicy) *HeartbeatLossHolds {
	return &HeartbeatLossHolds{holds: holds, policy: policy}
}

func (h *HeartbeatLossHolds) Evaluate(ctx context.Context, now time.Time) (domain.HeartbeatLossHold, error) {
	hold := domain.HeartbeatLossHold{Policy: h.policy, StartedAt: nil}

	startedAt, open, err := h.holds.FindOpen(ctx)
	if err != nil {
		return hold, fmt.Errorf("find open heartbeat loss hold: %w", err)
	}

	if open {
		hold.StartedAt = &startedAt
	}

	count, err := h.holds.CountAcrossTenants(ctx, h.policy.RecentSince(now), h.policy.LostBefore(now))
	if err != nil {
		return hold, fmt.Errorf("count heartbeat loss: %w", err)
	}

	end, start := h.policy.Decide(now, hold.StartedAt, count)

	if end {
		ended, err := h.holds.End(ctx, startedAt, now)
		if err != nil {
			return hold, fmt.Errorf("end heartbeat loss hold: %w", err)
		}

		if ended {
			slog.InfoContext(ctx, "heartbeat loss hold ended", "started_at", startedAt, "ended_at", now)
		}

		hold.StartedAt = nil
	}

	if !start {
		return hold, nil
	}

	started, err := h.holds.Start(ctx, now, count.Lost)
	if err != nil {
		return hold, fmt.Errorf("start heartbeat loss hold: %w", err)
	}

	if started {
		slog.InfoContext(ctx, "heartbeat loss hold started",
			"started_at", now, "device_count", count.Lost, "recent_device_count", count.Recent)
	}

	startedAt, open, err = h.holds.FindOpen(ctx)
	if err != nil {
		return hold, fmt.Errorf("find open heartbeat loss hold: %w", err)
	}

	if open {
		hold.StartedAt = &startedAt
	}

	return hold, nil
}

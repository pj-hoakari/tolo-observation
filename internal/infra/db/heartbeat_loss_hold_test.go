package db

import (
	"context"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestHeartbeatLossHoldCountsAcrossTenants(t *testing.T) {
	ctx := context.Background()
	devices := NewPostgresEdgeDeviceRepository(testDB)
	holds := NewPostgresHeartbeatLossHoldRepository(testDB)
	now := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)

	create := func(tenantPublicID string, sinceHeartbeat time.Duration, unregistered bool) {
		device := newDevice(t, tenantPublicID, "hold-count-event")
		heartbeatAt := now.Add(-sinceHeartbeat)
		device.LastHeartbeatAt = &heartbeatAt
		device.Unregistered = unregistered

		if err := devices.Create(ctx, device); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	create("tenant-hold-a", 10*time.Second, false)
	create("tenant-hold-b", 45*time.Second, false)
	create("tenant-hold-b", 61*time.Second, false)
	create("tenant-hold-a", 45*time.Second, true)

	got, err := holds.CountAcrossTenants(withTenant(ctx, "tenant-hold-a"), now.Add(-time.Minute), now.Add(-30*time.Second))
	if err != nil {
		t.Fatalf("CountAcrossTenants: %v", err)
	}

	if want := (domain.HeartbeatLossCount{Recent: 2, Lost: 1}); got != want {
		t.Errorf("CountAcrossTenants = %+v, want %+v", got, want)
	}
}

func TestHeartbeatLossHoldKeepsOneOpenHoldAndItsHistory(t *testing.T) {
	ctx := context.Background()
	holds := NewPostgresHeartbeatLossHoldRepository(testDB)
	first := time.Date(2100, time.February, 1, 0, 0, 0, 0, time.UTC)
	second := first.Add(10 * time.Minute)

	if started, err := holds.Start(ctx, first, 12); err != nil || !started {
		t.Fatalf("Start(first) = %v, %v, want true", started, err)
	}

	if started, err := holds.Start(ctx, first.Add(time.Second), 13); err != nil || started {
		t.Fatalf("Start while open = %v, %v, want false", started, err)
	}

	startedAt, open, err := holds.FindOpen(ctx)
	if err != nil || !open || !startedAt.Equal(first) {
		t.Fatalf("FindOpen = %v, %v, %v, want %v", startedAt, open, err, first)
	}

	if ended, err := holds.End(ctx, first, first.Add(5*time.Minute)); err != nil || !ended {
		t.Fatalf("End = %v, %v, want true", ended, err)
	}

	if ended, err := holds.End(ctx, first, first.Add(6*time.Minute)); err != nil || ended {
		t.Fatalf("End again = %v, %v, want false", ended, err)
	}

	if _, open, err := holds.FindOpen(ctx); err != nil || open {
		t.Fatalf("FindOpen after End = %v, %v, want closed", open, err)
	}

	if started, err := holds.Start(ctx, second, 11); err != nil || !started {
		t.Fatalf("Start(second) = %v, %v, want true", started, err)
	}

	type historyRow struct {
		StartedAt   time.Time  `db:"started_at"`
		EndedAt     *time.Time `db:"ended_at"`
		DeviceCount int        `db:"device_count"`
	}

	var history []historyRow
	if err := testDB.SelectContext(ctx, &history, `
		SELECT started_at, ended_at, device_count FROM heartbeat_loss_holds
		WHERE started_at >= $1 ORDER BY started_at`, first); err != nil {
		t.Fatalf("select history: %v", err)
	}

	if len(history) != 2 ||
		!history[0].StartedAt.Equal(first) || history[0].EndedAt == nil || !history[0].EndedAt.Equal(first.Add(5*time.Minute)) ||
		history[0].DeviceCount != 12 ||
		!history[1].StartedAt.Equal(second) || history[1].EndedAt != nil || history[1].DeviceCount != 11 {
		t.Errorf("history = %+v, want the ended first hold with 12 devices and the open second hold with 11", history)
	}

	if _, err := holds.End(ctx, second, second.Add(time.Minute)); err != nil {
		t.Fatalf("End(second): %v", err)
	}
}

package domain_test

import (
	"maps"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestObservationPointStatesKeepsHeldDevicesActive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-time.Minute)
	hold := domain.HeartbeatLossHold{
		Policy: domain.HeartbeatLossHoldPolicy{
			HeartbeatTimeout: 30 * time.Second,
			Threshold:        0.8,
			MinDevices:       10,
			MaxDuration:      5 * time.Minute,
		},
		StartedAt: &startedAt,
	}

	device := func(id domain.ObservationPointID, lastHeartbeatAt *time.Time, enabled, unregistered bool) domain.EdgeDevice {
		return domain.EdgeDevice{
			Unregistered:      unregistered,
			LastHeartbeatAt:   lastHeartbeatAt,
			ObservationPoints: []domain.ObservationPoint{{ID: id, Enabled: enabled}},
		}
	}
	at := func(d time.Duration) *time.Time {
		value := now.Add(d)

		return &value
	}

	got := domain.ObservationPointStates([]domain.EdgeDevice{
		device("online", at(-10*time.Second), true, false),
		device("held", at(-90*time.Second), true, false),
		device("lost-before-hold", at(-121*time.Second), true, false),
		device("never-seen", nil, true, false),
		device("disabled", at(-90*time.Second), false, false),
		device("unregistered", at(-90*time.Second), true, true),
	}, now, hold)

	want := map[domain.ObservationPointID]domain.ObservationPointState{
		"online":           domain.ObservationPointActive,
		"held":             domain.ObservationPointActive,
		"lost-before-hold": domain.ObservationPointOffline,
		"never-seen":       domain.ObservationPointOffline,
		"disabled":         domain.ObservationPointDisabled,
		"unregistered":     domain.ObservationPointDisabled,
	}
	if !maps.Equal(got, want) {
		t.Errorf("ObservationPointStates() = %v, want %v", got, want)
	}

	hold.StartedAt = nil
	if state := domain.ObservationPointStates([]domain.EdgeDevice{device("held", at(-90*time.Second), true, false)}, now, hold)["held"]; state != domain.ObservationPointOffline {
		t.Errorf("state without a hold = %v, want %v", state, domain.ObservationPointOffline)
	}
}

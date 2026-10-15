package domain

import "time"

type HeartbeatLossCount struct {
	Recent int
	Lost   int
}

type HeartbeatLossHoldPolicy struct {
	HeartbeatTimeout time.Duration
	Threshold        float64
	MinDevices       int
	MaxDuration      time.Duration
}

func (p HeartbeatLossHoldPolicy) RecentSince(now time.Time) time.Time {
	return now.Add(-2 * p.HeartbeatTimeout)
}

func (p HeartbeatLossHoldPolicy) LostBefore(now time.Time) time.Time {
	return now.Add(-p.HeartbeatTimeout)
}

func (p HeartbeatLossHoldPolicy) Decide(now time.Time, open *time.Time, count HeartbeatLossCount) (end, start bool) {
	enough := count.Recent >= p.MinDevices
	suspected := enough && float64(count.Lost) >= p.Threshold*float64(count.Recent)

	if open != nil {
		end = !now.Before(open.Add(p.MaxDuration)) || (enough && !suspected)
	}

	return end, (open == nil || end) && suspected
}

type HeartbeatLossHold struct {
	Policy    HeartbeatLossHoldPolicy
	StartedAt *time.Time
}

func (h HeartbeatLossHold) Connected(device EdgeDevice, now time.Time) bool {
	if device.Online(now, h.Policy.HeartbeatTimeout) {
		return true
	}

	if h.StartedAt == nil || device.Unregistered || device.LastHeartbeatAt == nil {
		return false
	}

	return !device.LastHeartbeatAt.Before(h.Policy.RecentSince(*h.StartedAt)) &&
		now.Before(h.StartedAt.Add(h.Policy.MaxDuration))
}

func ObservationPointStates(devices []EdgeDevice, now time.Time, hold HeartbeatLossHold) map[ObservationPointID]ObservationPointState {
	states := map[ObservationPointID]ObservationPointState{}

	for _, device := range devices {
		connected := hold.Connected(device, now)

		for _, point := range device.ObservationPoints {
			switch {
			case device.Unregistered || !point.Enabled:
				states[point.ID] = ObservationPointDisabled
			case !connected:
				states[point.ID] = ObservationPointOffline
			default:
				states[point.ID] = ObservationPointActive
			}
		}
	}

	return states
}

package domain_test

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestNewPublicIDs(t *testing.T) {
	t.Parallel()

	deviceID, err := domain.NewEdgeDeviceID()
	if err != nil {
		t.Fatalf("NewEdgeDeviceID: %v", err)
	}

	if !publicIDPattern.MatchString(string(deviceID)) {
		t.Errorf("edge device ID %q is not 16 lowercase hex characters", deviceID)
	}

	pointID, err := domain.NewObservationPointID()
	if err != nil {
		t.Fatalf("NewObservationPointID: %v", err)
	}

	if !publicIDPattern.MatchString(string(pointID)) {
		t.Errorf("observation point ID %q is not 16 lowercase hex characters", pointID)
	}

	other, err := domain.NewEdgeDeviceID()
	if err != nil {
		t.Fatalf("NewEdgeDeviceID: %v", err)
	}

	if other == deviceID {
		t.Errorf("two generated edge device IDs collided: %q", deviceID)
	}
}

func TestParsePublicIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid", value: "0123456789abcdef", wantErr: false},
		{name: "empty", value: "", wantErr: true},
		{name: "too short", value: "0123456789abcde", wantErr: true},
		{name: "too long", value: "0123456789abcdef0", wantErr: true},
		{name: "uppercase", value: "0123456789ABCDEF", wantErr: true},
		{name: "not hex", value: "0123456789abcdeg", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			deviceID, err := domain.ParseEdgeDeviceID(test.value)
			if test.wantErr {
				if !errors.Is(err, domain.ErrInvalidPublicID) {
					t.Errorf("ParseEdgeDeviceID(%q) error = %v, want ErrInvalidPublicID", test.value, err)
				}
			} else {
				if err != nil {
					t.Errorf("ParseEdgeDeviceID(%q): %v", test.value, err)
				}

				if string(deviceID) != test.value {
					t.Errorf("ParseEdgeDeviceID(%q) = %q", test.value, deviceID)
				}
			}

			pointID, err := domain.ParseObservationPointID(test.value)
			if test.wantErr {
				if !errors.Is(err, domain.ErrInvalidPublicID) {
					t.Errorf("ParseObservationPointID(%q) error = %v, want ErrInvalidPublicID", test.value, err)
				}
			} else {
				if err != nil {
					t.Errorf("ParseObservationPointID(%q): %v", test.value, err)
				}

				if string(pointID) != test.value {
					t.Errorf("ParseObservationPointID(%q) = %q", test.value, pointID)
				}
			}
		})
	}
}

func TestNewEdgeDevice(t *testing.T) {
	t.Parallel()

	device, err := domain.NewEdgeDevice("tenant-1", "event-1", " front gate ", []string{"in", "out"})
	if err != nil {
		t.Fatalf("NewEdgeDevice: %v", err)
	}

	if device.Name != "front gate" {
		t.Errorf("Name = %q, want trimmed %q", device.Name, "front gate")
	}

	if !publicIDPattern.MatchString(string(device.ID)) {
		t.Errorf("ID %q is not a public ID", device.ID)
	}

	if device.Unregistered || device.LastHeartbeatAt != nil {
		t.Errorf("new device is not freshly registered: %+v", device)
	}

	if len(device.ObservationPoints) != 2 {
		t.Fatalf("got %d observation points, want 2", len(device.ObservationPoints))
	}

	for i, point := range device.ObservationPoints {
		if !point.Enabled {
			t.Errorf("observation point %d is not enabled", i)
		}

		if !publicIDPattern.MatchString(string(point.ID)) {
			t.Errorf("observation point ID %q is not a public ID", point.ID)
		}
	}

	if device.ObservationPoints[0].ID == device.ObservationPoints[1].ID {
		t.Error("observation point IDs collided")
	}
}

func TestNewEdgeDeviceValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tenant     string
		event      string
		deviceName string
		points     []string
		wantErr    error
	}{
		{name: "no tenant", tenant: " ", event: "event-1", deviceName: "gate", points: nil, wantErr: domain.ErrTenantRequired},
		{name: "no event", tenant: "tenant-1", event: "", deviceName: "gate", points: nil, wantErr: domain.ErrEventRequired},
		{name: "no name", tenant: "tenant-1", event: "event-1", deviceName: "  ", points: nil, wantErr: domain.ErrEdgeDeviceNameRequired},
		{
			name: "no point name", tenant: "tenant-1", event: "event-1", deviceName: "gate",
			points: []string{"in", " "}, wantErr: domain.ErrObservationPointNameRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := domain.NewEdgeDevice(test.tenant, test.event, test.deviceName, test.points)
			if !errors.Is(err, test.wantErr) {
				t.Errorf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestEdgeDeviceUnregister(t *testing.T) {
	t.Parallel()

	device := newTestDevice(t)

	if err := device.Unregister(); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if !device.Unregistered {
		t.Error("device is not marked unregistered")
	}

	for i, point := range device.ObservationPoints {
		if point.Enabled {
			t.Errorf("observation point %d is still enabled", i)
		}
	}

	if err := device.Unregister(); !errors.Is(err, domain.ErrEdgeDeviceUnregistered) {
		t.Errorf("second Unregister error = %v, want ErrEdgeDeviceUnregistered", err)
	}
}

func TestEdgeDeviceUpdateObservationPoint(t *testing.T) {
	t.Parallel()

	device := newTestDevice(t)
	target := device.ObservationPoints[1].ID

	updated, err := device.UpdateObservationPoint(target, " north lane ", false)
	if err != nil {
		t.Fatalf("UpdateObservationPoint: %v", err)
	}

	if updated.Name != "north lane" || updated.Enabled {
		t.Errorf("updated point = %+v", updated)
	}

	if device.ObservationPoints[1].Name != "north lane" || device.ObservationPoints[1].Enabled {
		t.Errorf("device point not updated: %+v", device.ObservationPoints[1])
	}

	if _, err := device.UpdateObservationPoint("0123456789abcdef", "x", true); !errors.Is(err, domain.ErrObservationPointNotFound) {
		t.Errorf("unknown point error = %v, want ErrObservationPointNotFound", err)
	}

	if _, err := device.UpdateObservationPoint(target, " ", true); !errors.Is(err, domain.ErrObservationPointNameRequired) {
		t.Errorf("empty name error = %v, want ErrObservationPointNameRequired", err)
	}

	if err := device.Unregister(); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if _, err := device.UpdateObservationPoint(target, "x", true); !errors.Is(err, domain.ErrEdgeDeviceUnregistered) {
		t.Errorf("unregistered device error = %v, want ErrEdgeDeviceUnregistered", err)
	}
}

func TestEdgeDeviceOnline(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	timeout := 30 * time.Second

	tests := []struct {
		name         string
		heartbeatAgo *time.Duration
		unregistered bool
		want         bool
	}{
		{name: "no heartbeat", heartbeatAgo: nil, unregistered: false, want: false},
		{name: "within timeout", heartbeatAgo: durationPtr(timeout - time.Nanosecond), unregistered: false, want: true},
		{name: "exactly at timeout", heartbeatAgo: durationPtr(timeout), unregistered: false, want: true},
		{name: "past timeout", heartbeatAgo: durationPtr(timeout + time.Nanosecond), unregistered: false, want: false},
		{name: "unregistered", heartbeatAgo: durationPtr(0), unregistered: true, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			device := newTestDevice(t)
			device.Unregistered = test.unregistered

			if test.heartbeatAgo != nil {
				heartbeat := now.Add(-*test.heartbeatAgo)
				device.LastHeartbeatAt = &heartbeat
			}

			if got := device.Online(now, timeout); got != test.want {
				t.Errorf("Online() = %t, want %t", got, test.want)
			}
		})
	}
}

func durationPtr(d time.Duration) *time.Duration { return &d }

func newTestDevice(t *testing.T) domain.EdgeDevice {
	t.Helper()

	device, err := domain.NewEdgeDevice("tenant-1", "event-1", "front gate", []string{"in", "out"})
	if err != nil {
		t.Fatalf("NewEdgeDevice: %v", err)
	}

	return device
}

func TestEdgeDeviceHeartbeat(t *testing.T) {
	t.Parallel()

	device := newTestDevice(t)
	now := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	active := device.ObservationPoints[1].ID

	if err := device.Heartbeat(now, []domain.ObservationPointID{active}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	if device.LastHeartbeatAt == nil || !device.LastHeartbeatAt.Equal(now) {
		t.Errorf("LastHeartbeatAt = %v, want %v", device.LastHeartbeatAt, now)
	}

	if device.ObservationPoints[0].LastActiveAt != nil {
		t.Errorf("inactive observation point LastActiveAt = %v, want nil", device.ObservationPoints[0].LastActiveAt)
	}

	if device.ObservationPoints[1].LastActiveAt == nil || !device.ObservationPoints[1].LastActiveAt.Equal(now) {
		t.Errorf("active observation point LastActiveAt = %v, want %v", device.ObservationPoints[1].LastActiveAt, now)
	}
}

func TestEdgeDeviceHeartbeatRejects(t *testing.T) {
	t.Parallel()

	device := newTestDevice(t)
	now := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)

	unknown, err := domain.NewObservationPointID()
	if err != nil {
		t.Fatalf("NewObservationPointID: %v", err)
	}

	err = device.Heartbeat(now, []domain.ObservationPointID{device.ObservationPoints[0].ID, unknown})
	if !errors.Is(err, domain.ErrObservationPointNotFound) {
		t.Errorf("Heartbeat with an unknown observation point error = %v, want ErrObservationPointNotFound", err)
	}

	if device.LastHeartbeatAt != nil || device.ObservationPoints[0].LastActiveAt != nil {
		t.Error("a rejected heartbeat updated the device")
	}

	if err := device.Unregister(); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if err := device.Heartbeat(now, nil); !errors.Is(err, domain.ErrEdgeDeviceUnregistered) {
		t.Errorf("Heartbeat on an unregistered device error = %v, want ErrEdgeDeviceUnregistered", err)
	}

	if device.LastHeartbeatAt != nil {
		t.Error("a heartbeat on an unregistered device updated LastHeartbeatAt")
	}
}

func TestEdgeDeviceHasObservationPoint(t *testing.T) {
	t.Parallel()

	device := newTestDevice(t)

	if !device.HasObservationPoint(device.ObservationPoints[0].ID) {
		t.Error("HasObservationPoint() = false for an owned observation point")
	}

	if device.HasObservationPoint("0123456789abcdef") {
		t.Error("HasObservationPoint() = true for a foreign observation point")
	}
}

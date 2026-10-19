package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"

	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

const (
	testTenantPublicID = "a1b2c3d4e5f60718"
	testEventPublicID  = "0f1e2d3c4b5a6978"
	otherEventPublicID = "9876543210fedcba"
)

var testNow = time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)

// withEventToken returns the context the interceptor leaves behind for an
// event token scoped to eventPublicID.
func withEventToken(eventPublicID string) context.Context {
	return internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{
		TokenUse:       internaljwt.TokenUseEventAccess,
		TenantPublicID: testTenantPublicID,
		EventPublicID:  eventPublicID,
	})
}

var testHoldPolicy = domain.HeartbeatLossHoldPolicy{
	HeartbeatTimeout: 2 * time.Minute,
	Threshold:        0.8,
	MinDevices:       10,
	MaxDuration:      5 * time.Minute,
}

func newService(t *testing.T) (*application.EdgeDeviceService, *MockEdgeDeviceRepository) {
	t.Helper()

	service, devices, holds := newServiceWithHolds(t)
	holds.EXPECT().FindOpen(gomock.Any()).Return(time.Time{}, false, nil).AnyTimes()
	holds.EXPECT().CountAcrossTenants(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(domain.HeartbeatLossCount{}, nil).AnyTimes()

	return service, devices
}

func newServiceWithHolds(
	t *testing.T,
) (*application.EdgeDeviceService, *MockEdgeDeviceRepository, *MockHeartbeatLossHoldRepository) {
	t.Helper()

	controller := gomock.NewController(t)
	devices := NewMockEdgeDeviceRepository(controller)
	holds := NewMockHeartbeatLossHoldRepository(controller)

	return application.NewEdgeDeviceService(
		devices,
		application.NewHeartbeatLossHolds(holds, testHoldPolicy),
		application.EdgeDeviceConfig{
			HeartbeatTimeout: testHoldPolicy.HeartbeatTimeout,
			Now:              func() time.Time { return testNow },
		},
	), devices, holds
}

func TestRegisterEdgeDeviceBuildsObservationPagePath(t *testing.T) {
	t.Parallel()

	service, devices := newService(t)
	devices.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	output, err := service.RegisterEdgeDevice(withEventToken(testEventPublicID), application.RegisterEdgeDeviceInput{
		EventID:               testEventPublicID,
		Name:                  "gate-1",
		ObservationPointNames: []string{"north"},
	})
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	if got, want := output.ObservationPagePath, "/event/"+testEventPublicID+"/observation/"+string(output.Device.Device.ID); got != want {
		t.Errorf("ObservationPagePath = %q, want %q", got, want)
	}

	if got, want := output.Device.Device.TenantPublicID, testTenantPublicID; got != want {
		t.Errorf("TenantPublicID = %q, want %q", got, want)
	}
}

func TestRegisterEdgeDeviceRejectsAnotherEventsToken(t *testing.T) {
	t.Parallel()

	service, _ := newService(t)

	_, err := service.RegisterEdgeDevice(withEventToken(otherEventPublicID), application.RegisterEdgeDeviceInput{
		EventID:               testEventPublicID,
		Name:                  "gate-1",
		ObservationPointNames: nil,
	})
	if !errors.Is(err, tenantctx.ErrEventMismatch) {
		t.Fatalf("RegisterEdgeDevice() error = %v, want %v", err, tenantctx.ErrEventMismatch)
	}
}

func TestUnregisterEdgeDeviceRejectsAnotherEventsDevice(t *testing.T) {
	t.Parallel()

	service, devices := newService(t)
	devices.EXPECT().
		FindByID(gomock.Any(), domain.EdgeDeviceID("1122334455667788")).
		Return(domain.EdgeDevice{EventID: otherEventPublicID}, nil)

	_, err := service.UnregisterEdgeDevice(withEventToken(testEventPublicID), application.UnregisterEdgeDeviceInput{
		EventID:      testEventPublicID,
		EdgeDeviceID: "1122334455667788",
	})
	if !errors.Is(err, application.ErrForeignEvent) {
		t.Fatalf("UnregisterEdgeDevice() error = %v, want %v", err, application.ErrForeignEvent)
	}
}

func TestListEdgeDevicesDerivesLivenessFromHeartbeat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		lastHeartbeatAt *time.Time
		unregistered    bool
		want            bool
	}{
		{name: "fresh heartbeat", lastHeartbeatAt: ptr(testNow.Add(-time.Minute)), want: true},
		{name: "heartbeat at the timeout", lastHeartbeatAt: ptr(testNow.Add(-2 * time.Minute)), want: true},
		{name: "stale heartbeat", lastHeartbeatAt: ptr(testNow.Add(-2*time.Minute - time.Second)), want: false},
		{name: "never seen", lastHeartbeatAt: nil, want: false},
		{name: "unregistered with fresh heartbeat", lastHeartbeatAt: ptr(testNow), unregistered: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service, devices := newService(t)
			devices.EXPECT().
				ListByEvent(gomock.Any(), testEventPublicID, false).
				Return([]domain.EdgeDevice{{
					ID:              "1122334455667788",
					EventID:         testEventPublicID,
					Unregistered:    tt.unregistered,
					LastHeartbeatAt: tt.lastHeartbeatAt,
					ObservationPoints: []domain.ObservationPoint{
						{ID: "99aabbccddeeff00", Name: "north", Enabled: true},
					},
				}}, nil)

			got, err := service.ListEdgeDevices(withEventToken(testEventPublicID), application.ListEdgeDevicesInput{
				EventID:             testEventPublicID,
				IncludeUnregistered: false,
			})
			if err != nil {
				t.Fatalf("ListEdgeDevices() error = %v", err)
			}

			if len(got) != 1 || len(got[0].Device.ObservationPoints) != 1 {
				t.Fatalf("ListEdgeDevices() = %v, want one device with one point", got)
			}

			if online := got[0].Online; online != tt.want {
				t.Errorf("Online = %v, want %v", online, tt.want)
			}

			if enabled := got[0].Device.ObservationPoints[0].Enabled; enabled != tt.want {
				t.Errorf("Enabled = %v, want %v", enabled, tt.want)
			}
		})
	}
}

func TestUpdateObservationPointConfigDerivesEnabledFromHeartbeat(t *testing.T) {
	t.Parallel()

	service, devices := newService(t)
	devices.EXPECT().
		FindByObservationPointID(gomock.Any(), domain.ObservationPointID("99aabbccddeeff00")).
		Return(domain.EdgeDevice{
			ID:              "1122334455667788",
			EventID:         testEventPublicID,
			LastHeartbeatAt: ptr(testNow.Add(-10 * time.Minute)),
			ObservationPoints: []domain.ObservationPoint{
				{ID: "99aabbccddeeff00", Name: "north", Enabled: false},
			},
		}, nil)
	devices.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil)

	point, err := service.UpdateObservationPointConfig(
		withEventToken(testEventPublicID),
		application.UpdateObservationPointConfigInput{
			EventID:            testEventPublicID,
			ObservationPointID: "99aabbccddeeff00",
			Name:               "north gate",
			Enabled:            true,
		},
	)
	if err != nil {
		t.Fatalf("UpdateObservationPointConfig() error = %v", err)
	}

	if got, want := point.Name, "north gate"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}

	// The configuration now says enabled, but the device has gone quiet, so the
	// effective value the caller sees is false.
	if point.Enabled {
		t.Errorf("Enabled = true, want false")
	}
}

func TestHeartbeatMarksTheDeviceAndItsActivePoints(t *testing.T) {
	t.Parallel()

	service, devices := newService(t)
	devices.EXPECT().
		FindByID(gomock.Any(), domain.EdgeDeviceID("1122334455667788")).
		Return(domain.EdgeDevice{
			ID:      "1122334455667788",
			EventID: testEventPublicID,
			ObservationPoints: []domain.ObservationPoint{
				{ID: "99aabbccddeeff00", Name: "north", Enabled: true},
				{ID: "00ffeeddccbbaa99", Name: "south", Enabled: true},
			},
		}, nil)

	var saved domain.EdgeDevice

	devices.EXPECT().
		SaveHeartbeat(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, device domain.EdgeDevice) error {
			saved = device

			return nil
		})

	err := service.Heartbeat(withEventToken(testEventPublicID), application.HeartbeatInput{
		EventID:                   testEventPublicID,
		EdgeDeviceID:              "1122334455667788",
		ActiveObservationPointIDs: []string{"99aabbccddeeff00"},
	})
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if saved.LastHeartbeatAt == nil || !saved.LastHeartbeatAt.Equal(testNow) {
		t.Errorf("LastHeartbeatAt = %v, want %v", saved.LastHeartbeatAt, testNow)
	}

	if saved.ObservationPoints[0].LastActiveAt == nil {
		t.Errorf("north LastActiveAt = nil, want %v", testNow)
	}

	if saved.ObservationPoints[1].LastActiveAt != nil {
		t.Errorf("south LastActiveAt = %v, want nil", saved.ObservationPoints[1].LastActiveAt)
	}
}

func TestHeartbeatRejectsAnotherEventsDevice(t *testing.T) {
	t.Parallel()

	service, devices := newService(t)
	devices.EXPECT().
		FindByID(gomock.Any(), domain.EdgeDeviceID("1122334455667788")).
		Return(domain.EdgeDevice{EventID: otherEventPublicID}, nil)

	err := service.Heartbeat(withEventToken(testEventPublicID), application.HeartbeatInput{
		EventID:                   testEventPublicID,
		EdgeDeviceID:              "1122334455667788",
		ActiveObservationPointIDs: nil,
	})
	if !errors.Is(err, application.ErrForeignEvent) {
		t.Fatalf("Heartbeat() error = %v, want %v", err, application.ErrForeignEvent)
	}
}

func ptr(t time.Time) *time.Time {
	return &t
}

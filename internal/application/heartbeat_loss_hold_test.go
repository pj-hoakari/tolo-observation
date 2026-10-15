package application_test

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestListEdgeDevicesHoldsDisconnectionDuringSimultaneousHeartbeatLoss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		openSince       *time.Time
		count           domain.HeartbeatLossCount
		lastHeartbeatAt time.Time
		wantStart       bool
		wantEnd         bool
		wantOnline      bool
		wantEnabled     bool
	}{
		{
			name:            "starts a hold when the lost share reaches the threshold and holds a device lost in the window",
			count:           domain.HeartbeatLossCount{Recent: 10, Lost: 8},
			lastHeartbeatAt: testNow.Add(-3 * time.Minute),
			wantStart:       true,
			wantEnabled:     true,
		},
		{
			name:            "does not start a hold below the minimum number of devices",
			count:           domain.HeartbeatLossCount{Recent: 9, Lost: 9},
			lastHeartbeatAt: testNow.Add(-3 * time.Minute),
		},
		{
			name:            "does not start a hold below the threshold",
			count:           domain.HeartbeatLossCount{Recent: 10, Lost: 7},
			lastHeartbeatAt: testNow.Add(-3 * time.Minute),
		},
		{
			name:            "keeps the hold after the lost devices age out of the count",
			openSince:       ptr(testNow.Add(-2 * time.Minute)),
			count:           domain.HeartbeatLossCount{Recent: 0, Lost: 0},
			lastHeartbeatAt: testNow.Add(-5 * time.Minute),
			wantEnabled:     true,
		},
		{
			name:            "does not hold a device whose loss began before the window of the hold start",
			openSince:       ptr(testNow.Add(-time.Minute)),
			count:           domain.HeartbeatLossCount{Recent: 10, Lost: 8},
			lastHeartbeatAt: testNow.Add(-5*time.Minute - time.Second),
		},
		{
			name:            "ends the hold at the maximum duration and disconnects the device",
			openSince:       ptr(testNow.Add(-5 * time.Minute)),
			count:           domain.HeartbeatLossCount{Recent: 0, Lost: 0},
			lastHeartbeatAt: testNow.Add(-6 * time.Minute),
			wantEnd:         true,
		},
		{
			name:            "ends the hold when enough devices are back and disconnects a device still lost",
			openSince:       ptr(testNow.Add(-time.Minute)),
			count:           domain.HeartbeatLossCount{Recent: 10, Lost: 2},
			lastHeartbeatAt: testNow.Add(-3 * time.Minute),
			wantEnd:         true,
		},
		{
			name:            "reports a device whose heartbeat came back as online during the hold",
			openSince:       ptr(testNow.Add(-time.Minute)),
			count:           domain.HeartbeatLossCount{Recent: 10, Lost: 9},
			lastHeartbeatAt: testNow.Add(-30 * time.Second),
			wantOnline:      true,
			wantEnabled:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service, devices, holds := newServiceWithHolds(t, "https://{tenant_id}.example.test/event/{event_id}/observation/{edge_device_id}")

			devices.EXPECT().
				ListByEvent(gomock.Any(), testEventPublicID, false).
				Return([]domain.EdgeDevice{{
					ID:              "1122334455667788",
					EventID:         testEventPublicID,
					LastHeartbeatAt: ptr(tt.lastHeartbeatAt),
					ObservationPoints: []domain.ObservationPoint{
						{ID: "99aabbccddeeff00", Name: "north", Enabled: true},
					},
				}}, nil)

			if tt.openSince != nil {
				holds.EXPECT().FindOpen(gomock.Any()).Return(*tt.openSince, true, nil)
			} else {
				holds.EXPECT().FindOpen(gomock.Any()).Return(time.Time{}, false, nil)
			}

			holds.EXPECT().
				CountAcrossTenants(gomock.Any(), testNow.Add(-4*time.Minute), testNow.Add(-2*time.Minute)).
				Return(tt.count, nil)

			if tt.wantEnd {
				holds.EXPECT().End(gomock.Any(), *tt.openSince, testNow).Return(true, nil)
			}

			if tt.wantStart {
				holds.EXPECT().Start(gomock.Any(), testNow, tt.count.Lost).Return(true, nil)
				holds.EXPECT().FindOpen(gomock.Any()).Return(testNow, true, nil)
			}

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

			if online := got[0].Online; online != tt.wantOnline {
				t.Errorf("Online = %v, want %v", online, tt.wantOnline)
			}

			if enabled := got[0].Device.ObservationPoints[0].Enabled; enabled != tt.wantEnabled {
				t.Errorf("Enabled = %v, want %v", enabled, tt.wantEnabled)
			}
		})
	}
}

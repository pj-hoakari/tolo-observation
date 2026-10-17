package connect

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1/observationv1connect"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

const testEdgeDeviceID = "1122334455667788"

// fakeEdgeDeviceUseCases stands in for the application layer: it answers with
// what the test put in it, so the handler's mapping is what gets exercised.
type fakeEdgeDeviceUseCases struct {
	registerOutput application.RegisterEdgeDeviceOutput
	device         application.EdgeDeviceView
	devices        []application.EdgeDeviceView
	point          domain.ObservationPoint
	err            error
}

func (f *fakeEdgeDeviceUseCases) Heartbeat(_ context.Context, _ application.HeartbeatInput) error {
	return f.err
}

func (f *fakeEdgeDeviceUseCases) RegisterEdgeDevice(
	_ context.Context,
	_ application.RegisterEdgeDeviceInput,
) (application.RegisterEdgeDeviceOutput, error) {
	return f.registerOutput, f.err
}

func (f *fakeEdgeDeviceUseCases) UnregisterEdgeDevice(
	_ context.Context,
	_ application.UnregisterEdgeDeviceInput,
) (application.EdgeDeviceView, error) {
	return f.device, f.err
}

func (f *fakeEdgeDeviceUseCases) ListEdgeDevices(
	_ context.Context,
	_ application.ListEdgeDevicesInput,
) ([]application.EdgeDeviceView, error) {
	return f.devices, f.err
}

func (f *fakeEdgeDeviceUseCases) UpdateObservationPointConfig(
	_ context.Context,
	_ application.UpdateObservationPointConfigInput,
) (domain.ObservationPoint, error) {
	return f.point, f.err
}

func newEdgeDeviceClient(
	t *testing.T,
	scope string,
	useCases application.EdgeDeviceUseCases,
) observationv1connect.EdgeDeviceServiceClient {
	t.Helper()

	authorization, keys := mintEventToken(t, scope)
	httpServer := httptest.NewServer(newTestHandlerWithEdgeDevices(t, keys, useCases))
	t.Cleanup(httpServer.Close)

	return observationv1connect.NewEdgeDeviceServiceClient(connectrpc.NewClient(
		connecthttp.NewTransport(httpServer.Client(), httpServer.URL),
		authorizationInterceptor(authorization),
	))
}

func authorizationInterceptor(authorization string) connectrpc.ClientInterceptor {
	return func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
		return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
			if info, ok := connectrpc.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", authorization)
			}

			return next(ctx, spec)
		}
	}
}

func TestRegisterEdgeDevice(t *testing.T) {
	t.Parallel()

	useCases := &fakeEdgeDeviceUseCases{
		registerOutput: application.RegisterEdgeDeviceOutput{
			Device: application.EdgeDeviceView{
				Device: domain.EdgeDevice{
					ID:      domain.EdgeDeviceID(testEdgeDeviceID),
					EventID: testEventPublicID,
					Name:    "gate-1",
					ObservationPoints: []domain.ObservationPoint{
						{ID: domain.ObservationPointID("99aabbccddeeff00"), Name: "north", Enabled: true},
					},
				},
				Online: false,
			},
			ObservationPageURL: "https://example.test/observe/" + testEdgeDeviceID,
		},
	}

	client := newEdgeDeviceClient(t, "events.manage", useCases)

	res, err := client.RegisterEdgeDevice(context.Background(), &observationv1.RegisterEdgeDeviceRequest{
		EventId:               testEventPublicID,
		Name:                  "gate-1",
		ObservationPointNames: []string{"north"},
	})
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	if got, want := res.GetObservationPageUrl(), useCases.registerOutput.ObservationPageURL; got != want {
		t.Errorf("observation_page_url = %q, want %q", got, want)
	}

	device := res.GetDevice()
	if got, want := device.GetEdgeDeviceId(), testEdgeDeviceID; got != want {
		t.Errorf("edge_device_id = %q, want %q", got, want)
	}

	points := device.GetObservationPoints()
	if len(points) != 1 {
		t.Fatalf("observation_points = %v, want one point", points)
	}

	if got, want := points[0].GetMethod(), observationv1.MeasurementMethod_MEASUREMENT_METHOD_CAMERA; got != want {
		t.Errorf("method = %v, want %v", got, want)
	}

	if !points[0].GetEnabled() {
		t.Errorf("enabled = false, want true")
	}
}

func TestListEdgeDevicesReportsLiveness(t *testing.T) {
	t.Parallel()

	heartbeatAt := time.Date(2026, time.October, 11, 9, 30, 0, 0, time.UTC)

	tests := []struct {
		name          string
		view          application.EdgeDeviceView
		wantOnline    bool
		wantHeartbeat *time.Time
	}{
		{
			name:          "heartbeat within the timeout",
			view:          application.EdgeDeviceView{Device: domain.EdgeDevice{LastHeartbeatAt: &heartbeatAt}, Online: true},
			wantOnline:    true,
			wantHeartbeat: &heartbeatAt,
		},
		{
			name:          "heartbeat past the timeout",
			view:          application.EdgeDeviceView{Device: domain.EdgeDevice{LastHeartbeatAt: &heartbeatAt}, Online: false},
			wantOnline:    false,
			wantHeartbeat: &heartbeatAt,
		},
		{
			name:          "never heartbeated",
			view:          application.EdgeDeviceView{Device: domain.EdgeDevice{}, Online: false},
			wantOnline:    false,
			wantHeartbeat: nil,
		},
		{
			name: "unregistered",
			view: application.EdgeDeviceView{
				Device: domain.EdgeDevice{Unregistered: true, LastHeartbeatAt: &heartbeatAt},
				Online: false,
			},
			wantOnline:    false,
			wantHeartbeat: &heartbeatAt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.view.Device.ID = domain.EdgeDeviceID(testEdgeDeviceID)
			client := newEdgeDeviceClient(t, "events.read", &fakeEdgeDeviceUseCases{
				devices: []application.EdgeDeviceView{tt.view},
			})

			res, err := client.ListEdgeDevices(context.Background(), &observationv1.ListEdgeDevicesRequest{EventId: testEventPublicID, IncludeUnregistered: true})
			if err != nil {
				t.Fatalf("ListEdgeDevices() error = %v", err)
			}

			devices := res.GetDevices()
			if len(devices) != 1 {
				t.Fatalf("devices = %v, want one device", devices)
			}

			if got := devices[0].GetOnline(); got != tt.wantOnline {
				t.Errorf("online = %v, want %v", got, tt.wantOnline)
			}

			got := devices[0].GetLastHeartbeatAt()
			switch {
			case tt.wantHeartbeat == nil && got != nil:
				t.Errorf("last_heartbeat_at = %v, want unset", got.AsTime())
			case tt.wantHeartbeat != nil && (got == nil || !got.AsTime().Equal(*tt.wantHeartbeat)):
				t.Errorf("last_heartbeat_at = %v, want %v", got, *tt.wantHeartbeat)
			}
		})
	}
}

func TestEdgeDeviceErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want connectrpc.Code
	}{
		{name: "another event's device", err: application.ErrForeignEvent, want: connectrpc.CodePermissionDenied},
		{name: "unknown device", err: repository.ErrEdgeDeviceNotFound, want: connectrpc.CodeNotFound},
		{name: "malformed public ID", err: domain.ErrInvalidPublicID, want: connectrpc.CodeInvalidArgument},
		{name: "already unregistered", err: domain.ErrEdgeDeviceUnregistered, want: connectrpc.CodeFailedPrecondition},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newEdgeDeviceClient(t, "events.manage", &fakeEdgeDeviceUseCases{err: tt.err})

			_, err := client.UnregisterEdgeDevice(context.Background(), &observationv1.UnregisterEdgeDeviceRequest{
				EventId:      testEventPublicID,
				EdgeDeviceId: testEdgeDeviceID,
			})
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("UnregisterEdgeDevice() error code = %v (%v), want %v", got, err, tt.want)
			}
		})
	}
}

func TestEdgeDeviceErrorsAnswerFixedMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantCode    connectrpc.Code
		wantMessage string
	}{
		{
			name:        "sentinel wrapped by the application",
			err:         fmt.Errorf("find edge device: %w", repository.ErrEdgeDeviceNotFound),
			wantCode:    connectrpc.CodeNotFound,
			wantMessage: "edge device not found",
		},
		{
			name: "sentinel wrapped with a downstream error",
			err: fmt.Errorf("call GetQrLocations: %w: %w", application.ErrForeignEvent,
				connectrpc.NewError(connectrpc.CodePermissionDenied, "graph revision 42 belongs to tenant acme")),
			wantCode:    connectrpc.CodePermissionDenied,
			wantMessage: "resource belongs to another event",
		},
		{
			name: "sentinel joined with a database error",
			err: fmt.Errorf("unregister edge device: %w", errors.Join(
				errors.New(`ERROR: relation "edge_devices" is locked (SQLSTATE 55P03)`), domain.ErrEdgeDeviceUnregistered)),
			wantCode:    connectrpc.CodeFailedPrecondition,
			wantMessage: "edge device is unregistered",
		},
		{
			name:        "canceled request",
			err:         fmt.Errorf("find edge device: %w", context.Canceled),
			wantCode:    connectrpc.CodeCanceled,
			wantMessage: "canceled",
		},
		{
			name:        "timed out request",
			err:         fmt.Errorf("find edge device: %w", context.DeadlineExceeded),
			wantCode:    connectrpc.CodeDeadlineExceeded,
			wantMessage: "deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newEdgeDeviceClient(t, "events.manage", &fakeEdgeDeviceUseCases{err: tt.err})

			_, err := client.UnregisterEdgeDevice(context.Background(), &observationv1.UnregisterEdgeDeviceRequest{
				EventId:      testEventPublicID,
				EdgeDeviceId: testEdgeDeviceID,
			})

			var connectErr *connectrpc.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("UnregisterEdgeDevice() error = %v, want a connect error", err)
			}

			if got := connectErr.Code(); got != tt.wantCode {
				t.Errorf("Code() = %v, want %v", got, tt.wantCode)
			}

			if got := connectErr.Message(); got != tt.wantMessage {
				t.Errorf("Message() = %q, want %q", got, tt.wantMessage)
			}
		})
	}
}

type fakeMeasurementIngestUseCases struct {
	accepted int32
	err      error
}

func (f *fakeMeasurementIngestUseCases) ReportMeasurements(
	_ context.Context,
	_ application.ReportMeasurementsInput,
) (int32, error) {
	return f.accepted, f.err
}

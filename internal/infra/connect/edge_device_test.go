package connect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	registerInput  application.RegisterEdgeDeviceInput
	device         application.EdgeDeviceView
	devices        []application.EdgeDeviceView
	point          domain.ObservationPoint
	points         []domain.ObservationPoint
	err            error
}

func (f *fakeEdgeDeviceUseCases) ListEdgeDeviceObservationPoints(
	_ context.Context,
	_ application.ListEdgeDeviceObservationPointsInput,
) ([]domain.ObservationPoint, error) {
	return f.points, f.err
}

func (f *fakeEdgeDeviceUseCases) Heartbeat(_ context.Context, _ application.HeartbeatInput) error {
	return f.err
}

func (f *fakeEdgeDeviceUseCases) RegisterEdgeDevice(
	_ context.Context,
	input application.RegisterEdgeDeviceInput,
) (application.RegisterEdgeDeviceOutput, error) {
	f.registerInput = input

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
			ObservationPagePath: "/event/" + testEventPublicID + "/observation/" + testEdgeDeviceID,
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

	if got, want := res.GetObservationPagePath(), "/event/"+testEventPublicID+"/observation/"+testEdgeDeviceID; got != want {
		t.Errorf("observation_page_path = %q, want %q", got, want)
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

			if devices[0].Online == nil {
				t.Error("online is unset for an observation page")
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

func TestListEdgeDeviceObservationPointsAnswersTheEdgeDevice(t *testing.T) {
	t.Parallel()

	client := newEdgeDeviceClient(t, "events.report", &fakeEdgeDeviceUseCases{
		points: []domain.ObservationPoint{
			{ID: "99aabbccddeeff00", Name: "north", Enabled: true},
			{ID: "00ffeeddccbbaa99", Name: "south", Enabled: false},
		},
	})

	res, err := client.ListEdgeDeviceObservationPoints(
		context.Background(),
		&observationv1.ListEdgeDeviceObservationPointsRequest{EventId: testEventPublicID, EdgeDeviceId: testEdgeDeviceID},
	)
	if err != nil {
		t.Fatalf("ListEdgeDeviceObservationPoints() error = %v", err)
	}

	points := res.GetObservationPoints()
	if len(points) != 2 {
		t.Fatalf("observation_points = %v, want two points", points)
	}

	if got := points[0]; got.GetObservationPointId() != "99aabbccddeeff00" || got.GetName() != "north" || !got.GetEnabled() {
		t.Errorf("observation_points[0] = %v, want enabled north", got)
	}

	if got := points[1]; got.GetObservationPointId() != "00ffeeddccbbaa99" || got.GetName() != "south" || got.GetEnabled() {
		t.Errorf("observation_points[1] = %v, want disabled south", got)
	}
}

func TestListEdgeDeviceObservationPointsRejectsAnUnregisteredDevice(t *testing.T) {
	t.Parallel()

	client := newEdgeDeviceClient(t, "events.report", &fakeEdgeDeviceUseCases{
		err: fmt.Errorf("list edge device observation points: %w", domain.ErrEdgeDeviceUnregistered),
	})

	_, err := client.ListEdgeDeviceObservationPoints(
		context.Background(),
		&observationv1.ListEdgeDeviceObservationPointsRequest{EventId: testEventPublicID, EdgeDeviceId: testEdgeDeviceID},
	)
	if got := connectrpc.CodeOf(err); got != connectrpc.CodeFailedPrecondition {
		t.Fatalf("ListEdgeDeviceObservationPoints() error code = %v (%v), want %v", got, err, connectrpc.CodeFailedPrecondition)
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
		{name: "camera operation", err: domain.ErrEdgeDeviceCameraUnsupported, want: connectrpc.CodeFailedPrecondition},
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

func TestRegisterCameraEdgeDevice(t *testing.T) {
	t.Parallel()

	useCases := &fakeEdgeDeviceUseCases{
		registerOutput: application.RegisterEdgeDeviceOutput{
			Device: application.EdgeDeviceView{
				Device: domain.EdgeDevice{
					ID:      domain.EdgeDeviceID(testEdgeDeviceID),
					EventID: testEventPublicID,
					Name:    "camera-1",
					Kind:    domain.EdgeDeviceKindCamera,
					CameraLinks: []domain.CameraLink{{
						CameraEdgeDeviceID: testEdgeDeviceID,
						ObservationPointID: "99aabbccddeeff00",
					}},
				},
				Online: false,
			},
		},
	}

	client := newEdgeDeviceClient(t, "events.manage", useCases)

	res, err := client.RegisterEdgeDevice(context.Background(), &observationv1.RegisterEdgeDeviceRequest{
		EventId:                  testEventPublicID,
		Name:                     "camera-1",
		Kind:                     observationv1.EdgeDeviceKind_EDGE_DEVICE_KIND_CAMERA,
		LinkedObservationPointId: "99aabbccddeeff00",
	})
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	if got := useCases.registerInput.Kind; got != domain.EdgeDeviceKindCamera {
		t.Errorf("kind passed to the use case = %v, want camera", got)
	}

	if got := useCases.registerInput.LinkedObservationPointID; got != "99aabbccddeeff00" {
		t.Errorf("linked point passed to the use case = %q", got)
	}

	if got := res.GetObservationPagePath(); got != "" {
		t.Errorf("observation_page_path = %q, want empty for a camera", got)
	}

	device := res.GetDevice()
	if got := device.GetKind(); got != observationv1.EdgeDeviceKind_EDGE_DEVICE_KIND_CAMERA {
		t.Errorf("kind = %v, want camera", got)
	}

	if device.Online != nil {
		t.Errorf("online = %v, want unset for a camera", *device.Online)
	}

	if device.GetLastHeartbeatAt() != nil {
		t.Errorf("last_heartbeat_at = %v, want unset for a camera", device.GetLastHeartbeatAt())
	}

	links := device.GetCameraLinks()
	if len(links) != 1 || links[0].GetCameraEdgeDeviceId() != testEdgeDeviceID ||
		links[0].GetObservationPointId() != "99aabbccddeeff00" {
		t.Errorf("camera_links = %v, want the linked observation point", links)
	}
}

func TestListEdgeDevicesReportsCamerasWithoutOnlineOrHeartbeat(t *testing.T) {
	t.Parallel()

	heartbeatAt := time.Date(2026, time.October, 11, 9, 30, 0, 0, time.UTC)
	client := newEdgeDeviceClient(t, "events.read", &fakeEdgeDeviceUseCases{
		devices: []application.EdgeDeviceView{{
			Device: domain.EdgeDevice{
				ID:              domain.EdgeDeviceID(testEdgeDeviceID),
				Kind:            domain.EdgeDeviceKindCamera,
				LastHeartbeatAt: &heartbeatAt,
				CameraLinks: []domain.CameraLink{{
					CameraEdgeDeviceID: testEdgeDeviceID,
					ObservationPointID: "99aabbccddeeff00",
				}},
			},
			Online: true,
		}},
	})

	res, err := client.ListEdgeDevices(context.Background(), &observationv1.ListEdgeDevicesRequest{EventId: testEventPublicID})
	if err != nil {
		t.Fatalf("ListEdgeDevices() error = %v", err)
	}

	devices := res.GetDevices()
	if len(devices) != 1 {
		t.Fatalf("devices = %v, want one device", devices)
	}

	device := devices[0]
	if got := device.GetKind(); got != observationv1.EdgeDeviceKind_EDGE_DEVICE_KIND_CAMERA {
		t.Errorf("kind = %v, want camera", got)
	}

	if device.Online != nil {
		t.Errorf("online = %v, want unset for a camera", *device.Online)
	}

	if device.GetLastHeartbeatAt() != nil {
		t.Errorf("last_heartbeat_at = %v, want unset for a camera", device.GetLastHeartbeatAt())
	}

	if len(device.GetCameraLinks()) != 1 {
		t.Errorf("camera_links = %v, want one link", device.GetCameraLinks())
	}
}

func TestCameraOnlyRPCsFailPrecondition(t *testing.T) {
	t.Parallel()

	client := newEdgeDeviceClient(t, "events.report", &fakeEdgeDeviceUseCases{
		err: fmt.Errorf("camera edge device: %w", domain.ErrEdgeDeviceCameraUnsupported),
	})

	if _, err := client.Heartbeat(context.Background(), &observationv1.HeartbeatRequest{
		EventId: testEventPublicID, EdgeDeviceId: testEdgeDeviceID,
	}); connectrpc.CodeOf(err) != connectrpc.CodeFailedPrecondition {
		t.Errorf("Heartbeat() code = %v (%v), want failed_precondition", connectrpc.CodeOf(err), err)
	}

	if _, err := client.ListEdgeDeviceObservationPoints(
		context.Background(),
		&observationv1.ListEdgeDeviceObservationPointsRequest{EventId: testEventPublicID, EdgeDeviceId: testEdgeDeviceID},
	); connectrpc.CodeOf(err) != connectrpc.CodeFailedPrecondition {
		t.Errorf("ListEdgeDeviceObservationPoints() code = %v (%v), want failed_precondition", connectrpc.CodeOf(err), err)
	}
}

func TestReportMeasurementsRejectsACameraEdgeDevice(t *testing.T) {
	t.Parallel()

	authorization, keys := mintEventToken(t, "events.report")

	routes, err := RoutesWithVerifier(
		newTestVerifier(t, keys),
		&fakeEdgeDeviceUseCases{},
		&fakeMeasurementIngestUseCases{err: fmt.Errorf("camera edge device: %w", domain.ErrEdgeDeviceCameraUnsupported)},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("RoutesWithVerifier() error = %v", err)
	}

	mux := http.NewServeMux()
	routes(mux)

	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	client := observationv1connect.NewMeasurementIngestServiceClient(connectrpc.NewClient(
		connecthttp.NewTransport(httpServer.Client(), httpServer.URL),
		authorizationInterceptor(authorization),
	))

	_, err = client.ReportMeasurements(context.Background(), &observationv1.ReportMeasurementsRequest{
		EventId:      testEventPublicID,
		EdgeDeviceId: testEdgeDeviceID,
		Measurements: []*observationv1.Measurement{{
			ObservationPointId: "99aabbccddeeff00",
			Source:             observationv1.MeasurementSource_MEASUREMENT_SOURCE_EDGE,
		}},
	})
	if got := connectrpc.CodeOf(err); got != connectrpc.CodeFailedPrecondition {
		t.Fatalf("ReportMeasurements() error code = %v (%v), want failed_precondition", got, err)
	}
}

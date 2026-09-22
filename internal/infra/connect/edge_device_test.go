package connect

import (
	"context"
	"net/http/httptest"
	"testing"

	connectrpc "connectrpc.com/connect"

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
	device         domain.EdgeDevice
	devices        []domain.EdgeDevice
	point          domain.ObservationPoint
	err            error
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
) (domain.EdgeDevice, error) {
	return f.device, f.err
}

func (f *fakeEdgeDeviceUseCases) ListEdgeDevices(
	_ context.Context,
	_ application.ListEdgeDevicesInput,
) ([]domain.EdgeDevice, error) {
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

	return observationv1connect.NewEdgeDeviceServiceClient(
		httpServer.Client(),
		httpServer.URL,
		connectrpc.WithInterceptors(authorizationInterceptor(authorization)),
	)
}

func authorizationInterceptor(authorization string) connectrpc.UnaryInterceptorFunc {
	return func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			req.Header().Set("Authorization", authorization)

			return next(ctx, req)
		}
	}
}

func TestRegisterEdgeDevice(t *testing.T) {
	t.Parallel()

	useCases := &fakeEdgeDeviceUseCases{
		registerOutput: application.RegisterEdgeDeviceOutput{
			Device: domain.EdgeDevice{
				ID:      domain.EdgeDeviceID(testEdgeDeviceID),
				EventID: testEventPublicID,
				Name:    "gate-1",
				ObservationPoints: []domain.ObservationPoint{
					{ID: domain.ObservationPointID("99aabbccddeeff00"), Name: "north", Enabled: true},
				},
			},
			ObservationPageURL: "https://example.test/observe/" + testEdgeDeviceID,
		},
	}

	client := newEdgeDeviceClient(t, "events.manage", useCases)

	res, err := client.RegisterEdgeDevice(context.Background(), connectrpc.NewRequest(
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               testEventPublicID,
			Name:                  "gate-1",
			ObservationPointNames: []string{"north"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	if got, want := res.Msg.GetObservationPageUrl(), useCases.registerOutput.ObservationPageURL; got != want {
		t.Errorf("observation_page_url = %q, want %q", got, want)
	}

	device := res.Msg.GetDevice()
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

			_, err := client.UnregisterEdgeDevice(context.Background(), connectrpc.NewRequest(
				&observationv1.UnregisterEdgeDeviceRequest{
					EventId:      testEventPublicID,
					EdgeDeviceId: testEdgeDeviceID,
				},
			))
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("UnregisterEdgeDevice() error code = %v (%v), want %v", got, err, tt.want)
			}
		})
	}
}

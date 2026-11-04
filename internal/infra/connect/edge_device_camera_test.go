package connect

import (
	"context"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	dbinfra "github.com/pj-hoakari/tolo-observation/internal/infra/db"
)

func registerObservationPage(
	t *testing.T,
	flow observationFlow,
	eventID, name string,
) *observationv1.RegisterEdgeDeviceResponse {
	t.Helper()

	page, err := flow.edgeDevices.RegisterEdgeDevice(authorized(context.Background(),
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               eventID,
			Name:                  name,
			ObservationPointNames: []string{"north"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice(page) error = %v", err)
	}

	return page
}

func TestRegisterEdgeDeviceRejectsAnUnknownKind(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()
	eventID := "c0ffee00c0ffee01"

	_, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               eventID,
			Name:                  "unknown-kind",
			ObservationPointNames: []string{"north"},
			Kind:                  observationv1.EdgeDeviceKind(99),
		},
	))
	if got := connectrpc.CodeOf(err); got != connectrpc.CodeInvalidArgument {
		t.Fatalf("RegisterEdgeDevice(unknown kind) error code = %v (%v), want %v", got, err, connectrpc.CodeInvalidArgument)
	}
}

func TestCameraRegistrationRejectsAnUnregisteredObservationPage(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()
	eventID := "c0ffee00c0ffee00"

	page := registerObservationPage(t, flow, eventID, "gate-unregistered-camera")
	deviceID := page.GetDevice().GetEdgeDeviceId()
	pointID := page.GetDevice().GetObservationPoints()[0].GetObservationPointId()

	if _, err := flow.edgeDevices.UnregisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.UnregisterEdgeDeviceRequest{EventId: eventID, EdgeDeviceId: deviceID},
	)); err != nil {
		t.Fatalf("UnregisterEdgeDevice() error = %v", err)
	}

	_, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:                  eventID,
			Name:                     "camera-1",
			Kind:                     observationv1.EdgeDeviceKind_EDGE_DEVICE_KIND_CAMERA,
			LinkedObservationPointId: pointID,
		},
	))
	if got := connectrpc.CodeOf(err); got != connectrpc.CodeFailedPrecondition {
		t.Fatalf("RegisterEdgeDevice(camera) error code = %v (%v), want %v", got, err, connectrpc.CodeFailedPrecondition)
	}

	devices, err := flow.edgeDevices.ListEdgeDevices(authorized(ctx,
		flow.token(t, eventID, "events.read"),
		&observationv1.ListEdgeDevicesRequest{EventId: eventID, IncludeUnregistered: true},
	))
	if err != nil {
		t.Fatalf("ListEdgeDevices() error = %v", err)
	}

	for _, device := range devices.GetDevices() {
		if device.GetKind() == observationv1.EdgeDeviceKind_EDGE_DEVICE_KIND_CAMERA {
			t.Errorf("registered device = %v, want no camera after the rejected registration", device)
		}
	}
}

func TestConcurrentUnregisterAndCameraRegistrationRejectsAnUnregisteredPage(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()
	eventID := "c0ffee00c0ffee01"

	page := registerObservationPage(t, flow, eventID, "gate-concurrent-camera")
	pageID := domain.EdgeDeviceID(page.GetDevice().GetEdgeDeviceId())
	pointID := domain.ObservationPointID(page.GetDevice().GetObservationPoints()[0].GetObservationPointId())

	devices := dbinfra.NewPostgresEdgeDeviceRepository(testDB)
	tenantCtx := internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{
		TokenUse:       internaljwt.TokenUseEventAccess,
		TenantPublicID: testTenantPublicID,
		EventPublicID:  eventID,
	})

	unregisterLocked := make(chan struct{})
	releaseUnregister := make(chan struct{})

	unregisterDone := make(chan error, 1)

	go func() {
		unregisterDone <- devices.WithinTransaction(tenantCtx, func(txCtx context.Context) error {
			device, err := devices.FindByIDForUpdate(txCtx, pageID)
			if err != nil {
				return err
			}

			if err := device.Unregister(); err != nil {
				return err
			}

			if err := devices.Save(txCtx, device); err != nil {
				return err
			}

			close(unregisterLocked)

			<-releaseUnregister

			return nil
		})
	}()

	select {
	case <-unregisterLocked:
	case err := <-unregisterDone:
		t.Fatalf("unregister transaction failed before holding the page lock: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the unregister transaction to lock the page")
	}

	registerCtx, request := authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:                  eventID,
			Name:                     "camera-1",
			Kind:                     observationv1.EdgeDeviceKind_EDGE_DEVICE_KIND_CAMERA,
			LinkedObservationPointId: string(pointID),
		},
	)

	registerDone := make(chan error, 1)

	go func() {
		_, err := flow.edgeDevices.RegisterEdgeDevice(registerCtx, request)
		registerDone <- err
	}()

	select {
	case err := <-registerDone:
		close(releaseUnregister)

		t.Fatalf("RegisterEdgeDevice() completed while the page was locked for unregister: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(releaseUnregister)

	if err := <-unregisterDone; err != nil {
		t.Fatalf("unregister transaction error = %v", err)
	}

	err := <-registerDone
	if got := connectrpc.CodeOf(err); got != connectrpc.CodeFailedPrecondition {
		t.Fatalf("RegisterEdgeDevice(camera) error code = %v (%v), want %v", got, err, connectrpc.CodeFailedPrecondition)
	}

	owner, err := devices.FindByObservationPointID(tenantCtx, pointID)
	if err != nil {
		t.Fatalf("FindByObservationPointID() error = %v", err)
	}

	if len(owner.CameraLinks) != 0 {
		t.Errorf("camera links = %v, want none for the unregistered page", owner.CameraLinks)
	}
}

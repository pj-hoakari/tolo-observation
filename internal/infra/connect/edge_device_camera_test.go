package connect

import (
	"context"
	"errors"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	dbinfra "github.com/pj-hoakari/tolo-observation/internal/infra/db"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

type failPageSaveRepository struct {
	repository.EdgeDeviceRepository
}

var errPageSave = errors.New("injected page save failure")

func (r failPageSaveRepository) Save(ctx context.Context, device domain.EdgeDevice) error {
	if device.Kind == domain.EdgeDeviceKindObservationPage {
		return errPageSave
	}

	return r.EdgeDeviceRepository.Save(ctx, device)
}

func TestUnregisterPageAlsoUnregistersLinkedCameras(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()
	eventID := "c0ffee00c0ffee02"

	page, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx, flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "page", ObservationPointNames: []string{"north", "south"}},
	))
	if err != nil {
		t.Fatal(err)
	}

	for _, point := range page.GetDevice().GetObservationPoints() {
		_, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx, flow.token(t, eventID, "events.manage"),
			&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "camera", Kind: observationv1.EdgeDeviceKind_EDGE_DEVICE_KIND_CAMERA, LinkedObservationPointId: point.GetObservationPointId()},
		))
		if err != nil {
			t.Fatal(err)
		}
	}

	result, err := flow.edgeDevices.UnregisterEdgeDevice(authorized(ctx, flow.token(t, eventID, "events.manage"),
		&observationv1.UnregisterEdgeDeviceRequest{EventId: eventID, EdgeDeviceId: page.GetDevice().GetEdgeDeviceId()},
	))
	if err != nil {
		t.Fatal(err)
	}

	if !result.GetDevice().GetUnregistered() || len(result.GetDevice().GetCameraLinks()) != 0 {
		t.Fatalf("unregistered page = %v", result.GetDevice())
	}

	for _, point := range result.GetDevice().GetObservationPoints() {
		if point.GetEnabled() {
			t.Errorf("point remains enabled: %v", point)
		}
	}

	devices, err := flow.edgeDevices.ListEdgeDevices(authorized(ctx, flow.token(t, eventID, "events.read"),
		&observationv1.ListEdgeDevicesRequest{EventId: eventID, IncludeUnregistered: true},
	))
	if err != nil {
		t.Fatal(err)
	}

	if len(devices.GetDevices()) != 3 {
		t.Fatalf("got %d devices, want 3", len(devices.GetDevices()))
	}

	for _, device := range devices.GetDevices() {
		if !device.GetUnregistered() || len(device.GetCameraLinks()) != 0 {
			t.Errorf("device remains registered or linked: %v", device)
		}
	}
}

func TestUnregisterPageRollsBackCameraUnregisterOnFailure(t *testing.T) {
	eventID := "c0ffee00c0ffee03"
	ctx := internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{
		TokenUse: internaljwt.TokenUseEventAccess, TenantPublicID: testTenantPublicID, EventPublicID: eventID,
	})
	devices := dbinfra.NewPostgresEdgeDeviceRepository(testDB)

	page, err := domain.NewEdgeDevice(testTenantPublicID, eventID, "page", []string{"north"})
	if err != nil {
		t.Fatal(err)
	}

	if err := devices.Create(ctx, page); err != nil {
		t.Fatal(err)
	}

	camera, err := domain.NewEdgeDeviceWithKind(testTenantPublicID, eventID, "camera", domain.EdgeDeviceKindCamera, nil, string(page.ObservationPoints[0].ID))
	if err != nil {
		t.Fatal(err)
	}

	if err := devices.Create(ctx, camera); err != nil {
		t.Fatal(err)
	}

	service := application.NewEdgeDeviceService(failPageSaveRepository{devices}, nil, application.EdgeDeviceConfig{})

	_, err = service.UnregisterEdgeDevice(ctx, application.UnregisterEdgeDeviceInput{EventID: eventID, EdgeDeviceID: string(page.ID)})
	if !errors.Is(err, errPageSave) {
		t.Fatalf("got %v, want injected error", err)
	}

	for _, id := range []domain.EdgeDeviceID{page.ID, camera.ID} {
		device, err := devices.FindByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}

		if device.Unregistered || len(device.CameraLinks) != 1 {
			t.Errorf("rollback left device changed: %+v", device)
		}
	}
}

func TestConcurrentCameraAndPageUnregister(t *testing.T) {
	eventID := "c0ffee00c0ffee04"
	ctx := internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{
		TokenUse: internaljwt.TokenUseEventAccess, TenantPublicID: testTenantPublicID, EventPublicID: eventID,
	})
	devices := dbinfra.NewPostgresEdgeDeviceRepository(testDB)

	page, err := domain.NewEdgeDevice(testTenantPublicID, eventID, "page", []string{"north"})
	if err != nil {
		t.Fatal(err)
	}

	if err := devices.Create(ctx, page); err != nil {
		t.Fatal(err)
	}

	camera, err := domain.NewEdgeDeviceWithKind(testTenantPublicID, eventID, "camera", domain.EdgeDeviceKindCamera, nil, string(page.ObservationPoints[0].ID))
	if err != nil {
		t.Fatal(err)
	}

	if err := devices.Create(ctx, camera); err != nil {
		t.Fatal(err)
	}

	locked := make(chan struct{})
	release := make(chan struct{})

	cameraDone := make(chan error, 1)
	go func() {
		cameraDone <- devices.WithinTransaction(ctx, func(txCtx context.Context) error {
			device, err := devices.FindByIDForUpdate(txCtx, camera.ID)
			if err != nil {
				return err
			}

			close(locked)
			<-release

			if err := device.Unregister(); err != nil {
				return err
			}

			return devices.Save(txCtx, device)
		})
	}()

	select {
	case <-locked:
	case err := <-cameraDone:
		t.Fatalf("camera lock failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for camera lock")
	}

	service := application.NewEdgeDeviceService(devices, nil, application.EdgeDeviceConfig{})
	pageDone := make(chan error, 1)

	go func() {
		_, err := service.UnregisterEdgeDevice(ctx, application.UnregisterEdgeDeviceInput{EventID: eventID, EdgeDeviceID: string(page.ID)})
		pageDone <- err
	}()

	select {
	case err := <-pageDone:
		close(release)
		t.Fatalf("page unregister completed before camera unlock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	if err := <-cameraDone; err != nil {
		t.Fatalf("camera unregister: %v", err)
	}

	if err := <-pageDone; err != nil {
		t.Fatalf("page unregister: %v", err)
	}

	for _, id := range []domain.EdgeDeviceID{page.ID, camera.ID} {
		device, err := devices.FindByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}

		if !device.Unregistered || len(device.CameraLinks) != 0 {
			t.Errorf("device remains registered or linked: %+v", device)
		}
	}
}

func TestPageUnregisterWaitsForCameraRegistration(t *testing.T) {
	eventID := "c0ffee00c0ffee05"
	ctx := internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{
		TokenUse: internaljwt.TokenUseEventAccess, TenantPublicID: testTenantPublicID, EventPublicID: eventID,
	})
	devices := dbinfra.NewPostgresEdgeDeviceRepository(testDB)

	page, err := domain.NewEdgeDevice(testTenantPublicID, eventID, "page", []string{"north"})
	if err != nil {
		t.Fatal(err)
	}

	if err := devices.Create(ctx, page); err != nil {
		t.Fatal(err)
	}

	camera, err := domain.NewEdgeDeviceWithKind(testTenantPublicID, eventID, "camera", domain.EdgeDeviceKindCamera, nil, string(page.ObservationPoints[0].ID))
	if err != nil {
		t.Fatal(err)
	}

	created := make(chan struct{})
	release := make(chan struct{})

	registrationDone := make(chan error, 1)
	go func() {
		registrationDone <- devices.WithinTransaction(ctx, func(txCtx context.Context) error {
			if _, err := devices.FindByObservationPointIDForUpdate(txCtx, page.ObservationPoints[0].ID); err != nil {
				return err
			}

			if err := devices.Create(txCtx, camera); err != nil {
				return err
			}

			close(created)
			<-release

			return nil
		})
	}()

	select {
	case <-created:
	case err := <-registrationDone:
		t.Fatalf("camera registration failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for camera registration")
	}

	service := application.NewEdgeDeviceService(devices, nil, application.EdgeDeviceConfig{})
	pageDone := make(chan error, 1)

	go func() {
		_, err := service.UnregisterEdgeDevice(ctx, application.UnregisterEdgeDeviceInput{EventID: eventID, EdgeDeviceID: string(page.ID)})
		pageDone <- err
	}()

	select {
	case err := <-pageDone:
		close(release)
		t.Fatalf("page unregister completed before camera registration committed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	if err := <-registrationDone; err != nil {
		t.Fatal(err)
	}

	if err := <-pageDone; err != nil {
		t.Fatal(err)
	}

	device, err := devices.FindByID(ctx, camera.ID)
	if err != nil {
		t.Fatal(err)
	}

	if !device.Unregistered || len(device.CameraLinks) != 0 {
		t.Errorf("camera remains registered or linked: %+v", device)
	}
}

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

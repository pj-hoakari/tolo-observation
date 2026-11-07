package db

import (
	"context"
	"errors"
	"testing"
	"time"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

func withTenant(ctx context.Context, tenantPublicID string) context.Context {
	return internaljwt.ContextWithClaims(ctx, internaljwt.Claims{TenantPublicID: tenantPublicID})
}

func newDevice(t *testing.T, tenantPublicID, eventID string, pointNames ...string) domain.EdgeDevice {
	t.Helper()

	device, err := domain.NewEdgeDevice(tenantPublicID, eventID, "front gate", pointNames)
	if err != nil {
		t.Fatalf("NewEdgeDevice: %v", err)
	}

	return device
}

func newCamera(t *testing.T, tenantPublicID, eventID string, pointID domain.ObservationPointID) domain.EdgeDevice {
	t.Helper()

	device, err := domain.NewEdgeDeviceWithKind(
		tenantPublicID,
		eventID,
		"camera-1",
		domain.EdgeDeviceKindCamera,
		nil,
		string(pointID),
	)
	if err != nil {
		t.Fatalf("NewEdgeDeviceWithKind: %v", err)
	}

	return device
}

func assertSameDevice(t *testing.T, got, want domain.EdgeDevice) {
	t.Helper()

	if got.ID != want.ID || got.TenantPublicID != want.TenantPublicID || got.EventID != want.EventID {
		t.Errorf("identity mismatch: got %+v, want %+v", got, want)
	}

	if got.Name != want.Name || got.Unregistered != want.Unregistered || got.Kind != want.Kind {
		t.Errorf("state mismatch: got %+v, want %+v", got, want)
	}

	switch {
	case got.LastHeartbeatAt == nil && want.LastHeartbeatAt != nil,
		got.LastHeartbeatAt != nil && want.LastHeartbeatAt == nil:
		t.Errorf("last heartbeat mismatch: got %v, want %v", got.LastHeartbeatAt, want.LastHeartbeatAt)
	case got.LastHeartbeatAt != nil && !got.LastHeartbeatAt.Equal(*want.LastHeartbeatAt):
		t.Errorf("last heartbeat = %v, want %v", got.LastHeartbeatAt, want.LastHeartbeatAt)
	}

	if len(got.ObservationPoints) != len(want.ObservationPoints) {
		t.Fatalf("got %d observation points, want %d", len(got.ObservationPoints), len(want.ObservationPoints))
	}

	for i, point := range want.ObservationPoints {
		if got.ObservationPoints[i].ID != point.ID ||
			got.ObservationPoints[i].Name != point.Name ||
			got.ObservationPoints[i].Enabled != point.Enabled {
			t.Errorf("observation point %d = %+v, want %+v", i, got.ObservationPoints[i], point)
		}
	}

	if len(got.CameraLinks) != len(want.CameraLinks) {
		t.Fatalf("got %d camera links, want %d", len(got.CameraLinks), len(want.CameraLinks))
	}

	for i, link := range want.CameraLinks {
		if got.CameraLinks[i] != link {
			t.Errorf("camera link %d = %+v, want %+v", i, got.CameraLinks[i], link)
		}
	}
}

func TestPostgresEdgeDeviceRepositoryRoundTrip(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-round-trip")
	device := newDevice(t, "tenant-round-trip", "event-round-trip", "in", "out")

	if err := repo.Create(ctx, device); err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := repo.FindByID(ctx, device.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	assertSameDevice(t, found, device)

	byPoint, err := repo.FindByObservationPointID(ctx, device.ObservationPoints[1].ID)
	if err != nil {
		t.Fatalf("FindByObservationPointID: %v", err)
	}

	assertSameDevice(t, byPoint, device)

	heartbeat := time.Now().UTC().Truncate(time.Microsecond)
	found.Name = "renamed gate"
	found.LastHeartbeatAt = &heartbeat

	if _, err := found.UpdateObservationPoint(device.ObservationPoints[0].ID, "renamed lane", false); err != nil {
		t.Fatalf("UpdateObservationPoint: %v", err)
	}

	if err := repo.Save(ctx, found); err != nil {
		t.Fatalf("Save: %v", err)
	}

	saved, err := repo.FindByID(ctx, device.ID)
	if err != nil {
		t.Fatalf("FindByID after Save: %v", err)
	}

	assertSameDevice(t, saved, found)

	devices, err := repo.ListByEvent(ctx, "event-round-trip", false)
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}

	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(devices))
	}

	assertSameDevice(t, devices[0], saved)
}

func TestPostgresEdgeDeviceRepositorySaveHeartbeatKeepsConfiguration(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-heartbeat")
	device := newDevice(t, "tenant-heartbeat", "event-heartbeat", "in", "out")

	if err := repo.Create(ctx, device); err != nil {
		t.Fatalf("Create: %v", err)
	}

	stale := device
	stale.Name = "stale name"
	stale.ObservationPoints = []domain.ObservationPoint{
		{ID: device.ObservationPoints[0].ID, Name: "stale lane", Enabled: false},
		{ID: device.ObservationPoints[1].ID, Name: "stale lane", Enabled: false},
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	stale.LastHeartbeatAt = &now
	stale.ObservationPoints[0].LastActiveAt = &now

	if err := repo.SaveHeartbeat(ctx, stale); err != nil {
		t.Fatalf("SaveHeartbeat: %v", err)
	}

	saved, err := repo.FindByID(ctx, device.ID)
	if err != nil {
		t.Fatalf("FindByID after SaveHeartbeat: %v", err)
	}

	want := device
	want.LastHeartbeatAt = &now
	assertSameDevice(t, saved, want)

	if saved.ObservationPoints[0].LastActiveAt == nil || !saved.ObservationPoints[0].LastActiveAt.Equal(now) {
		t.Errorf("in LastActiveAt = %v, want %v", saved.ObservationPoints[0].LastActiveAt, now)
	}

	if saved.ObservationPoints[1].LastActiveAt != nil {
		t.Errorf("out LastActiveAt = %v, want nil", saved.ObservationPoints[1].LastActiveAt)
	}

	unregistered := saved
	unregistered.Unregistered = true

	if err := repo.Save(ctx, unregistered); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := repo.SaveHeartbeat(ctx, unregistered); !errors.Is(err, repository.ErrEdgeDeviceNotFound) {
		t.Errorf("SaveHeartbeat on an unregistered device error = %v, want repository.ErrEdgeDeviceNotFound", err)
	}
}

func TestPostgresEdgeDeviceRepositoryListByEventExcludesUnregistered(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-list")
	eventID := "event-list"
	active := newDevice(t, "tenant-list", eventID, "in")
	retired := newDevice(t, "tenant-list", eventID, "out")

	if err := repo.Create(ctx, active); err != nil {
		t.Fatalf("Create active: %v", err)
	}

	if err := repo.Create(ctx, retired); err != nil {
		t.Fatalf("Create retired: %v", err)
	}

	if err := retired.Unregister(); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if err := repo.Save(ctx, retired); err != nil {
		t.Fatalf("Save: %v", err)
	}

	devices, err := repo.ListByEvent(ctx, eventID, false)
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}

	if len(devices) != 1 || devices[0].ID != active.ID {
		t.Fatalf("ListByEvent(includeUnregistered=false) = %+v, want only %q", devices, active.ID)
	}

	all, err := repo.ListByEvent(ctx, eventID, true)
	if err != nil {
		t.Fatalf("ListByEvent(includeUnregistered=true): %v", err)
	}

	if len(all) != 2 || all[0].ID != active.ID || all[1].ID != retired.ID {
		t.Fatalf("ListByEvent(includeUnregistered=true) = %+v, want both in insertion order", all)
	}

	if !all[1].Unregistered || all[1].ObservationPoints[0].Enabled {
		t.Errorf("unregistered device did not persist its disabled points: %+v", all[1])
	}
}

func TestPostgresEdgeDeviceRepositoryTenantBoundary(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-owner")
	device := newDevice(t, "tenant-owner", "event-boundary", "in")

	if err := repo.Create(ctx, device); err != nil {
		t.Fatalf("Create: %v", err)
	}

	otherCtx := withTenant(context.Background(), "tenant-intruder")

	if _, err := repo.FindByID(otherCtx, device.ID); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("FindByID with another tenant's claims error = %v, want tenantctx.ErrMismatch", err)
	}

	if _, err := repo.FindByObservationPointID(otherCtx, device.ObservationPoints[0].ID); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("FindByObservationPointID with another tenant's claims error = %v, want tenantctx.ErrMismatch", err)
	}

	if _, err := repo.ListByEvent(otherCtx, "event-boundary", true); !errors.Is(err, tenantctx.ErrMismatch) {
		t.Errorf("ListByEvent with another tenant's claims error = %v, want tenantctx.ErrMismatch", err)
	}
}

func TestPostgresEdgeDeviceRepositoryNotFound(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := context.Background()
	unknown := domain.EdgeDeviceID("00000000deadbeef")

	if _, err := repo.FindByID(ctx, unknown); !errors.Is(err, repository.ErrEdgeDeviceNotFound) {
		t.Errorf("FindByID(unknown) error = %v, want repository.ErrEdgeDeviceNotFound", err)
	}

	if _, err := repo.FindByObservationPointID(ctx, "00000000deadbeef"); !errors.Is(err, repository.ErrEdgeDeviceNotFound) {
		t.Errorf("FindByObservationPointID(unknown) error = %v, want repository.ErrEdgeDeviceNotFound", err)
	}

	missing := newDevice(t, "tenant-missing", "event-missing", "in")
	missing.ID = unknown

	if err := repo.Save(ctx, missing); !errors.Is(err, repository.ErrEdgeDeviceNotFound) {
		t.Errorf("Save(unknown) error = %v, want repository.ErrEdgeDeviceNotFound", err)
	}
}

func TestPostgresEdgeDeviceRepositoryCameraRoundTrip(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-camera")
	page := newDevice(t, "tenant-camera", "event-camera", "north")

	if err := repo.Create(ctx, page); err != nil {
		t.Fatalf("Create page: %v", err)
	}

	camera := newCamera(t, "tenant-camera", "event-camera", page.ObservationPoints[0].ID)

	if err := repo.Create(ctx, camera); err != nil {
		t.Fatalf("Create camera: %v", err)
	}

	found, err := repo.FindByID(ctx, camera.ID)
	if err != nil {
		t.Fatalf("FindByID camera: %v", err)
	}

	assertSameDevice(t, found, camera)

	pageWithLink, err := repo.FindByObservationPointID(ctx, page.ObservationPoints[0].ID)
	if err != nil {
		t.Fatalf("FindByObservationPointID: %v", err)
	}

	wantLink := domain.CameraLink{
		CameraEdgeDeviceID: camera.ID,
		ObservationPointID: page.ObservationPoints[0].ID,
	}
	pageWant := page
	pageWant.CameraLinks = []domain.CameraLink{wantLink}
	assertSameDevice(t, pageWithLink, pageWant)

	devices, err := repo.ListByEvent(ctx, "event-camera", false)
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}

	if len(devices) != 2 {
		t.Fatalf("ListByEvent returned %d devices, want 2", len(devices))
	}

	assertSameDevice(t, devices[0], pageWant)
	assertSameDevice(t, devices[1], camera)

	if err := found.Unregister(); err != nil {
		t.Fatalf("Unregister camera: %v", err)
	}

	if err := repo.Save(ctx, found); err != nil {
		t.Fatalf("Save unregistered camera: %v", err)
	}

	unregistered, err := repo.FindByID(ctx, camera.ID)
	if err != nil {
		t.Fatalf("FindByID unregistered camera: %v", err)
	}

	if !unregistered.Unregistered || len(unregistered.CameraLinks) != 0 {
		t.Errorf("unregistered camera = %+v, want unregistered without links", unregistered)
	}

	pageAfter, err := repo.FindByID(ctx, page.ID)
	if err != nil {
		t.Fatalf("FindByID page after camera unregister: %v", err)
	}

	if len(pageAfter.CameraLinks) != 0 {
		t.Errorf("page camera links = %v, want none after the camera unregistered", pageAfter.CameraLinks)
	}

	if len(pageAfter.ObservationPoints) != 1 || !pageAfter.ObservationPoints[0].Enabled {
		t.Errorf("page observation points = %+v, want the point still enabled", pageAfter.ObservationPoints)
	}
}

func TestEdgeDeviceReadsUseOneSnapshot(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-camera-snapshot")

	page := newDevice(t, "tenant-camera-snapshot", "event-camera-snapshot", "north")
	if err := repo.Create(ctx, page); err != nil {
		t.Fatal(err)
	}

	camera := newCamera(t, "tenant-camera-snapshot", "event-camera-snapshot", page.ObservationPoints[0].ID)

	_, err := readSnapshot(ctx, testDB, func(snapshot context.Context) (struct{}, error) {
		// Establish the snapshot before a camera is committed on another connection.
		before, err := repo.FindByID(snapshot, page.ID)
		if err != nil {
			return struct{}{}, err
		}

		if len(before.CameraLinks) != 0 {
			t.Fatalf("unexpected initial links: %v", before.CameraLinks)
		}

		if err := repo.Create(ctx, camera); err != nil {
			return struct{}{}, err
		}

		devices, err := repo.ListByEvent(snapshot, page.EventID, false)
		if err != nil {
			return struct{}{}, err
		}

		if len(devices) != 1 || len(devices[0].CameraLinks) != 0 {
			t.Errorf("snapshot saw a partial camera registration: %+v", devices)
		}

		owner, err := repo.FindByObservationPointID(snapshot, page.ObservationPoints[0].ID)
		if err != nil {
			return struct{}{}, err
		}

		if len(owner.CameraLinks) != 0 {
			t.Errorf("snapshot saw new link: %v", owner.CameraLinks)
		}

		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	devices, err := repo.ListByEvent(ctx, page.EventID, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(devices) != 2 || len(devices[0].CameraLinks) != 1 || len(devices[1].CameraLinks) != 1 {
		t.Errorf("fresh snapshot did not see the complete registration: %+v", devices)
	}
}

func TestPostgresEdgeDeviceRepositoryFindByObservationPointIDForUpdateSerializesWithUnregister(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-camera-lock")
	page := newDevice(t, "tenant-camera-lock", "event-camera-lock", "north")

	if err := repo.Create(ctx, page); err != nil {
		t.Fatalf("Create page: %v", err)
	}

	pointID := page.ObservationPoints[0].ID

	var contenderErr error

	err := repo.WithinTransaction(ctx, func(txCtx context.Context) error {
		locked, err := repo.FindByObservationPointIDForUpdate(txCtx, pointID)
		if err != nil {
			return err
		}

		if err := locked.Unregister(); err != nil {
			return err
		}

		if err := repo.Save(txCtx, locked); err != nil {
			return err
		}

		waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()

		contenderErr = repo.WithinTransaction(waitCtx, func(contenderCtx context.Context) error {
			_, err := repo.FindByObservationPointIDForUpdate(contenderCtx, pointID)

			return err
		})

		return nil
	})
	if err != nil {
		t.Fatalf("WithinTransaction() error = %v", err)
	}

	if contenderErr == nil {
		t.Error("FindByObservationPointIDForUpdate() while the page was locked for unregister succeeded, want it to wait")
	}

	unregistered, err := repo.FindByObservationPointID(ctx, pointID)
	if err != nil {
		t.Fatalf("FindByObservationPointID after unregister: %v", err)
	}

	if !unregistered.Unregistered {
		t.Errorf("page unregistered = false after the unregister transaction committed")
	}
}

func TestPostgresEdgeDeviceRepositoryRejectsASecondCameraOnTheSamePoint(t *testing.T) {
	repo := NewPostgresEdgeDeviceRepository(testDB)
	ctx := withTenant(context.Background(), "tenant-camera-unique")
	page := newDevice(t, "tenant-camera-unique", "event-camera-unique", "north")

	if err := repo.Create(ctx, page); err != nil {
		t.Fatalf("Create page: %v", err)
	}

	first := newCamera(t, "tenant-camera-unique", "event-camera-unique", page.ObservationPoints[0].ID)
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("Create first camera: %v", err)
	}

	second := newCamera(t, "tenant-camera-unique", "event-camera-unique", page.ObservationPoints[0].ID)
	if err := repo.Create(ctx, second); !errors.Is(err, domain.ErrObservationPointAlreadyLinked) {
		t.Fatalf("Create second camera error = %v, want ErrObservationPointAlreadyLinked", err)
	}
}

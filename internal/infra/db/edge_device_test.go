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

func assertSameDevice(t *testing.T, got, want domain.EdgeDevice) {
	t.Helper()

	if got.ID != want.ID || got.TenantPublicID != want.TenantPublicID || got.EventID != want.EventID {
		t.Errorf("identity mismatch: got %+v, want %+v", got, want)
	}

	if got.Name != want.Name || got.Unregistered != want.Unregistered {
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

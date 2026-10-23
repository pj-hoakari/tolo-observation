package connect

import (
	"context"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	dbinfra "github.com/pj-hoakari/tolo-observation/internal/infra/db"
)

const guestEventPublicID = "0a1b2c3d4e5f6071"

func TestGetGuestSnapshot(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()
	serviceToken := flow.serviceToken(t, "tolo-guest-service", testTenantPublicID)

	_, err := flow.statusQueries.GetGuestSnapshot(authorized(ctx, serviceToken,
		&observationv1.GetGuestSnapshotRequest{EventId: guestEventPublicID}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Fatalf("GetGuestSnapshot before any status error code = %v, want %v", got, want)
	}

	takenAt := time.Date(2026, time.October, 23, 10, 0, 0, 0, time.UTC)

	saved, err := dbinfra.NewPostgresGuestStatusRepository(testDB).SaveGuestStatus(ctx, testTenantPublicID, domain.GuestStatus{
		EventID: guestEventPublicID,
		TakenAt: takenAt,
		Crowd: []domain.CrowdStatus{
			{Anchor: domain.GraphAnchor{RouteID: "r1"}, Level: domain.CrowdLevelHigh, Danger: true},
		},
		Queues: []domain.QueueStatus{
			{QueueID: "q1", GatePointIDs: []string{"g1"}, WaitTimeMinutes: 7.5, TurnaroundCount: 2, RecommendedGatePointIDs: []string{"g1"}},
		},
	})
	if err != nil {
		t.Fatalf("SaveGuestStatus() error = %v", err)
	}

	got, err := flow.statusQueries.GetGuestSnapshot(authorized(ctx, serviceToken,
		&observationv1.GetGuestSnapshotRequest{EventId: guestEventPublicID}))
	if err != nil {
		t.Fatalf("GetGuestSnapshot() error = %v", err)
	}

	status := got.GetStatus()
	if status.GetEventId() != guestEventPublicID || status.GetSequence() != saved.Sequence || !status.GetTakenAt().AsTime().Equal(takenAt) {
		t.Errorf("status = %v, want event %s, sequence %d, taken_at %v", status, guestEventPublicID, saved.Sequence, takenAt)
	}

	crowd := status.GetCrowd()
	if len(crowd) != 1 || crowd[0].GetRouteId() != "r1" || crowd[0].GetLevel() != observationv1.CrowdLevel_CROWD_LEVEL_HIGH || !crowd[0].GetDanger() {
		t.Errorf("crowd = %v, want r1 high with danger", crowd)
	}

	queues := status.GetQueues()
	if len(queues) != 1 || queues[0].GetQueueId() != "q1" || queues[0].GetWaitTimeMinutes() != 7.5 || queues[0].GetTurnaroundCount() != 2 {
		t.Errorf("queues = %v, want q1 waiting 7.5 minutes with 2 turnarounds", queues)
	}

	_, err = flow.statusQueries.GetGuestSnapshot(authorized(ctx,
		flow.token(t, guestEventPublicID, "events.read"),
		&observationv1.GetGuestSnapshotRequest{EventId: guestEventPublicID}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
		t.Errorf("GetGuestSnapshot with an event access token error code = %v, want %v", got, want)
	}
}

package connect

import (
	"context"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	dbinfra "github.com/pj-hoakari/tolo-observation/internal/infra/db"
	"github.com/pj-hoakari/tolo-observation/internal/infra/pubsub"
)

const guestEventPublicID = "9e57a7e500000001"

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

func TestObservationCyclePublishesGuestStatus(t *testing.T) {
	const eventID = "5eed5eed5eed5eed"

	flow := newObservationFlow(t)
	ctx := context.Background()

	published, err := flow.guestStatuses.Subscribe(ctx, pubsub.GuestStatusTopic)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "gate-guest", ObservationPointNames: []string{"hall"}},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	hall := registered.GetDevice().GetObservationPoints()[0].GetObservationPointId()
	flow.graph.mapTo(hall, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p1"}})

	report := func(t *testing.T) {
		t.Helper()

		if _, err := flow.measurements.ReportMeasurements(authorized(ctx,
			flow.token(t, eventID, "events.report"),
			&observationv1.ReportMeasurementsRequest{
				EventId:      eventID,
				EdgeDeviceId: registered.GetDevice().GetEdgeDeviceId(),
				Measurements: []*observationv1.Measurement{edgeMeasurement(hall, flow.observedStart)},
			},
		)); err != nil {
			t.Fatalf("ReportMeasurements() error = %v", err)
		}
	}

	next := func(t *testing.T) *observationv1.GuestStatus {
		t.Helper()

		select {
		case msg := <-published:
			msg.Ack()

			if got := msg.Metadata.Get(pubsub.EventIDMetadataKey); got != eventID {
				t.Errorf("ordering metadata %s = %q, want %q", pubsub.EventIDMetadataKey, got, eventID)
			}

			var status observationv1.GuestStatus
			if err := proto.Unmarshal(msg.Payload, &status); err != nil {
				t.Fatalf("decode published guest status: %v", err)
			}

			return &status
		case <-time.After(5 * time.Second):
			t.Fatal("no guest status was published")

			return nil
		}
	}

	report(t)
	first := next(t)

	flow.flow.setUnavailable(true)
	report(t)
	second := next(t)

	if first.GetEventId() != eventID || first.GetTakenAt() == nil || len(first.GetCrowd()) != 0 || len(first.GetQueues()) != 0 {
		t.Errorf("first guest status = %v, want event %s with taken_at and no crowd or queues", first, eventID)
	}

	if second.GetSequence() != first.GetSequence()+1 {
		t.Errorf("sequence after a cycle whose optimization failed = %d, want %d", second.GetSequence(), first.GetSequence()+1)
	}

	if second.GetTakenAt().AsTime().Before(first.GetTakenAt().AsTime()) {
		t.Errorf("taken_at went back from %v to %v", first.GetTakenAt().AsTime(), second.GetTakenAt().AsTime())
	}

	snapshot, err := flow.statusQueries.GetGuestSnapshot(authorized(ctx,
		flow.serviceToken(t, "tolo-guest-service", testTenantPublicID),
		&observationv1.GetGuestSnapshotRequest{EventId: eventID}))
	if err != nil {
		t.Fatalf("GetGuestSnapshot() error = %v", err)
	}

	if !proto.Equal(snapshot.GetStatus(), second) {
		t.Errorf("GetGuestSnapshot() = %v, want the last published %v", snapshot.GetStatus(), second)
	}

	flow.flow.setUnavailable(false)

	if err := flow.guestStatuses.Close(); err != nil {
		t.Fatalf("close guest status broker: %v", err)
	}

	requestsBefore, _ := flow.flow.received()

	report(t)

	if requests, _ := flow.flow.received(); len(requests) != len(requestsBefore)+1 {
		t.Errorf("Optimize requests after a failed publish = %d, want %d", len(requests), len(requestsBefore)+1)
	}

	afterFailure, err := flow.statusQueries.GetGuestSnapshot(authorized(ctx,
		flow.serviceToken(t, "tolo-guest-service", testTenantPublicID),
		&observationv1.GetGuestSnapshotRequest{EventId: eventID}))
	if err != nil {
		t.Fatalf("GetGuestSnapshot() after a failed publish error = %v", err)
	}

	if got, want := afterFailure.GetStatus().GetSequence(), second.GetSequence()+1; got != want {
		t.Errorf("sequence after a failed publish = %d, want %d", got, want)
	}
}

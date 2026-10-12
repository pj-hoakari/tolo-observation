package connect

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	dbinfra "github.com/pj-hoakari/tolo-observation/internal/infra/db"
)

const arcHistoryEventPublicID = "0a1b2c3d4e5f6071"

func TestObservationCycleSendsArcHistory(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()
	snapshots := dbinfra.NewPostgresSnapshotRepository(testDB)
	now := time.Now().UTC().Truncate(time.Microsecond)

	seed := func(windowEnd time.Time, routeID string, stagnation float64) {
		t.Helper()

		err := snapshots.SaveSnapshot(ctx, testTenantPublicID, domain.Snapshot{
			ID:          fmt.Sprintf("seed-%s-%d", routeID, windowEnd.UnixMicro()),
			EventID:     arcHistoryEventPublicID,
			WindowStart: windowEnd.Add(-time.Minute),
			WindowEnd:   windowEnd,
			PointScores: nil,
			RouteScores: []domain.RouteScore{{RouteID: routeID, StagnationScore: &stagnation, Exhaustive: true}},
		})
		if err != nil {
			t.Fatalf("SaveSnapshot: %v", err)
		}
	}

	earliest := now.Add(-72*time.Hour - time.Minute)
	seed(now.Add(-72*time.Hour), "r1", 1000)

	for hour := 1; hour <= 6; hour++ {
		seed(now.Add(-time.Duration(7-hour)*time.Hour), "r1", float64(hour))
	}

	seed(now.Add(-3*time.Hour), "r2", 4)
	seed(now.Add(-2*time.Hour), "r2", 8)
	seed(now.Add(-2*time.Hour+time.Second), "r9", 7)

	if _, err := flow.settings.UpdateObservationSettings(ctx, authorized(
		flow.token(t, arcHistoryEventPublicID, "events.manage"),
		&observationv1.UpdateObservationSettingsRequest{
			EventId:  arcHistoryEventPublicID,
			Settings: &observationv1.ObservationSettings{HistoryWindowDays: 1, EventType: observationv1.EventType_EVENT_TYPE_LONG_TERM},
		},
	)); err != nil {
		t.Fatalf("UpdateObservationSettings() error = %v", err)
	}

	registered, err := flow.edgeDevices.RegisterEdgeDevice(ctx, authorized(
		flow.token(t, arcHistoryEventPublicID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               arcHistoryEventPublicID,
			Name:                  "gate-1",
			ObservationPointNames: []string{"corridor"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	deviceID := registered.Msg.GetDevice().GetEdgeDeviceId()
	corridor := registered.Msg.GetDevice().GetObservationPoints()[0].GetObservationPointId()
	flow.graph.mapTo(corridor, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})

	reportToken := flow.token(t, arcHistoryEventPublicID, "events.report")

	if _, err := flow.edgeDevices.Heartbeat(ctx, authorized(reportToken, &observationv1.HeartbeatRequest{
		EventId:                   arcHistoryEventPublicID,
		EdgeDeviceId:              deviceID,
		ActiveObservationPointIds: []string{corridor},
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	before := time.Now()

	if _, err := flow.measurements.ReportMeasurements(ctx, authorized(reportToken, &observationv1.ReportMeasurementsRequest{
		EventId:      arcHistoryEventPublicID,
		EdgeDeviceId: deviceID,
		Measurements: []*observationv1.Measurement{edgeMeasurement(corridor, flow.observedStart)},
	})); err != nil {
		t.Fatalf("ReportMeasurements() error = %v", err)
	}

	after := time.Now().Add(time.Microsecond)

	requests, _ := flow.flow.received()
	if len(requests) != 1 {
		t.Fatalf("Optimize received %d requests, want 1", len(requests))
	}

	hours := requests[0].GetEventContext().GetAvailableHistoryHours()
	if low, high := before.Sub(earliest).Hours(), after.Sub(earliest).Hours(); hours < low || hours > high {
		t.Errorf("available_history_hours = %v, want between %v and %v from the earliest stored snapshot", hours, low, high)
	}

	stats := map[string]*flowv1.ArcHistoryStat{}
	for _, stat := range requests[0].GetHistoryDigest().GetArcStats() {
		stats[stat.GetEdgeId()] = stat
	}

	if len(stats) != 2 || stats["r1"] == nil || stats["r2"] == nil {
		t.Fatalf("arc_stats = %v, want r1 and r2 only, without the edge outside the graph", requests[0].GetHistoryDigest().GetArcStats())
	}

	r1 := stats["r1"]
	if r1.P90Stagnation == nil || math.Abs(r1.GetP90Stagnation()-5.5) > 1e-9 ||
		r1.BaselineStagnation == nil || math.Abs(r1.GetBaselineStagnation()-3.5) > 1e-9 ||
		math.Abs(r1.GetAvailableSpanHours()-(5*time.Hour+time.Minute).Hours()) > 1e-9 || r1.FlowSensitivityEta != nil {
		t.Errorf("r1 arc stat = %v, want p90 5.5 and baseline 3.5 over the last day only, spanning 5h1m", r1)
	}

	r2 := stats["r2"]
	if r2.P90Stagnation != nil || r2.BaselineStagnation != nil ||
		math.Abs(r2.GetAvailableSpanHours()-(time.Hour+time.Minute).Hours()) > 1e-9 {
		t.Errorf("r2 arc stat = %v, want no p90 or baseline for a 1h1m span", r2)
	}
}

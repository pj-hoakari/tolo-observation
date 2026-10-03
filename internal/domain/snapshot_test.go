package domain_test

import (
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestNewSnapshot(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.October, 3, 10, 0, 0, 0, time.UTC)

	measurement := func(pointID string, offset time.Duration, countIn, countOut int32) domain.Measurement {
		m, err := domain.NewMeasurement(pointID, start.Add(offset), start.Add(offset+time.Minute), countIn, countOut)
		if err != nil {
			t.Fatalf("NewMeasurement: %v", err)
		}

		return m
	}

	const (
		routePointA = "00000000000000a1"
		routePointB = "00000000000000a2"
		nodePoint   = "00000000000000b1"
		unmapped    = "00000000000000c1"
	)

	measurements := []domain.Measurement{
		measurement(routePointA, 0, 100, 100),
		measurement(routePointA, time.Minute, 6, 2),
		measurement(routePointB, time.Minute, 4, 1),
		measurement(nodePoint, time.Minute, 9, 4),
		measurement(unmapped, time.Minute, 50, 50),
	}

	mappings := []domain.ObservationPointMapping{
		{ObservationPointID: routePointA, Anchor: domain.GraphAnchor{RouteID: "r1"}},
		{ObservationPointID: routePointB, Anchor: domain.GraphAnchor{RouteID: "r1"}},
		{ObservationPointID: nodePoint, Anchor: domain.GraphAnchor{PointID: "p1"}},
		{ObservationPointID: "00000000000000d1", Anchor: domain.GraphAnchor{RouteID: "r2"}},
	}

	snapshot, err := domain.NewSnapshot("event", start, start.Add(2*time.Minute), measurements, mappings)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	if len(snapshot.ID) != 16 {
		t.Errorf("snapshot ID = %q, want a 16 character public ID", snapshot.ID)
	}

	wantRoutes := []domain.RouteScore{{RouteID: "r1", Forward: 10, Backward: 3, Exhaustive: true}}
	if len(snapshot.RouteScores) != 1 || snapshot.RouteScores[0] != wantRoutes[0] {
		t.Errorf("route scores = %+v, want %+v", snapshot.RouteScores, wantRoutes)
	}

	wantPoints := []domain.PointScore{{PointID: "p1", OccupancyDelta: 5, Exhaustive: true}}
	if len(snapshot.PointScores) != 1 || snapshot.PointScores[0] != wantPoints[0] {
		t.Errorf("point scores = %+v, want %+v", snapshot.PointScores, wantPoints)
	}
}

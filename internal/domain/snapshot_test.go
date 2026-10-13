package domain_test

import (
	"fmt"
	"maps"
	"math"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestNewSnapshot(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.October, 3, 10, 0, 0, 0, time.UTC)

	measurement := func(pointID string, offset time.Duration, countIn, countOut int32, detected *float64) domain.Measurement {
		m, err := domain.NewMeasurement(pointID, start.Add(offset), start.Add(offset+time.Minute), countIn, countOut, detected)
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
		silentPoint = "00000000000000e1"
		emptyPoint  = "00000000000000f1"
	)

	measurements := []domain.Measurement{
		measurement(routePointA, 0, 100, 100, new(4.0)),
		measurement(routePointA, time.Minute, 6, 2, new(6.0)),
		measurement(routePointB, 0, 1, 1, nil),
		measurement(routePointB, time.Minute, 4, 1, new(2.0)),
		measurement(nodePoint, time.Minute, 9, 4, new(3.0)),
		measurement(unmapped, time.Minute, 50, 50, new(40.0)),
		measurement(silentPoint, time.Minute, 2, 1, nil),
		measurement(emptyPoint, time.Minute, 3, 0, new(0.0)),
	}

	mappings := []domain.ObservationPointMapping{
		{ObservationPointID: routePointA, Anchor: domain.GraphAnchor{RouteID: "r1"}},
		{ObservationPointID: routePointB, Anchor: domain.GraphAnchor{RouteID: "r1"}},
		{ObservationPointID: nodePoint, Anchor: domain.GraphAnchor{PointID: "p1"}},
		{ObservationPointID: "00000000000000d1", Anchor: domain.GraphAnchor{RouteID: "r2"}},
		{ObservationPointID: silentPoint, Anchor: domain.GraphAnchor{RouteID: "r3"}},
		{ObservationPointID: emptyPoint, Anchor: domain.GraphAnchor{RouteID: "r4"}},
	}

	snapshot, err := domain.NewSnapshot("event", start, start.Add(2*time.Minute), measurements, mappings)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	if len(snapshot.ID) != 16 {
		t.Errorf("snapshot ID = %q, want a 16 character public ID", snapshot.ID)
	}

	gotRoutes := map[string]string{}
	for _, score := range snapshot.RouteScores {
		gotRoutes[score.RouteID] = fmt.Sprintf("%v/%v/%v/%v", score.Forward, score.Backward, deref(score.StagnationScore), score.Exhaustive)
	}

	wantRoutes := map[string]string{
		"r1": "10/3/7/true",
		"r3": "2/1/<nil>/true",
		"r4": "3/0/0/true",
	}
	if !maps.Equal(gotRoutes, wantRoutes) {
		t.Errorf("route scores (forward/backward/stagnation/exhaustive) = %v, want %v", gotRoutes, wantRoutes)
	}

	wantPoints := []domain.PointScore{{PointID: "p1", PeopleScore: 3, OccupancyDelta: 5, Exhaustive: true}}
	if len(snapshot.PointScores) != 1 || snapshot.PointScores[0] != wantPoints[0] {
		t.Errorf("point scores = %+v, want %+v", snapshot.PointScores, wantPoints)
	}
}

func deref(value *float64) any {
	if value == nil {
		return nil
	}

	return *value
}

func arcHistoryFromSamples(samples [][2]float64) domain.ArcHistory {
	history := domain.ArcHistory{SampleCount: len(samples)}

	var stagnationSum float64

	for _, sample := range samples {
		flow, stagnation := sample[0], sample[1]
		history.FlowSum += flow
		history.FlowSquareSum += flow * flow
		history.FlowStagnationSum += flow * stagnation
		stagnationSum += stagnation
	}

	if len(samples) > 0 {
		history.MeanStagnation = stagnationSum / float64(len(samples))
	}

	return history
}

func linearSamples(count int, flowAt func(int) float64, stagnationAt func(float64) float64) [][2]float64 {
	samples := make([][2]float64, 0, count)
	for i := range count {
		flow := flowAt(i)
		samples = append(samples, [2]float64{flow, stagnationAt(flow)})
	}

	return samples
}

func TestArcHistoryFlowSensitivityEta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		samples [][2]float64
		wantEta float64
		wantOK  bool
	}{
		{
			name:    "negative linear relation recovers eta",
			samples: linearSamples(12, func(i int) float64 { return float64(i * 3) }, func(v float64) float64 { return 20 - 0.25*v }),
			wantEta: 0.25,
			wantOK:  true,
		},
		{
			name:    "positive slope",
			samples: linearSamples(12, func(i int) float64 { return float64(i) }, func(v float64) float64 { return 2 + 0.5*v }),
			wantOK:  false,
		},
		{
			name:    "flat stagnation",
			samples: linearSamples(12, func(i int) float64 { return float64(i) }, func(float64) float64 { return 5 }),
			wantOK:  false,
		},
		{
			name:    "constant flow",
			samples: linearSamples(12, func(int) float64 { return 7 }, func(float64) float64 { return 3 }),
			wantOK:  false,
		},
		{
			name:    "zero flow",
			samples: linearSamples(20, func(int) float64 { return 0 }, func(float64) float64 { return 3 }),
			wantOK:  false,
		},
		{
			name:    "too few samples",
			samples: linearSamples(11, func(i int) float64 { return float64(i) }, func(v float64) float64 { return 20 - v }),
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eta, ok := arcHistoryFromSamples(tt.samples).FlowSensitivityEta()
			if ok != tt.wantOK || (ok && math.Abs(eta-tt.wantEta) > 1e-9) {
				t.Errorf("FlowSensitivityEta() = %v, %v, want %v, %v", eta, ok, tt.wantEta, tt.wantOK)
			}
		})
	}
}

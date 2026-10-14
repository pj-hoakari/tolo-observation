package domain_test

import (
	"fmt"
	"maps"
	"math"
	"slices"
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
		{ObservationPointID: silentPoint, Anchor: domain.GraphAnchor{PointID: "p3"}},
		{ObservationPointID: emptyPoint, Anchor: domain.GraphAnchor{PointID: "p4"}},
	}

	active := domain.ObservationPointActive
	states := map[domain.ObservationPointID]domain.ObservationPointState{
		routePointA: active, routePointB: active, nodePoint: active, unmapped: active, silentPoint: active, emptyPoint: active,
	}

	snapshot, _, _, err := domain.NewSnapshot("event", start, start.Add(2*time.Minute), measurements, mappings, states)
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

	gotPoints := map[string]string{}
	for _, score := range snapshot.PointScores {
		gotPoints[score.PointID] = fmt.Sprintf("%v/%v/%v", deref(score.PeopleScore), score.OccupancyDelta, score.Exhaustive)
	}

	wantPoints := map[string]string{
		"p1": "3/5/true",
		"p3": "<nil>/1/true",
		"p4": "0/3/true",
	}
	if !maps.Equal(gotPoints, wantPoints) {
		t.Errorf("point scores (people/occupancy delta/exhaustive) = %v, want %v", gotPoints, wantPoints)
	}
}

func TestNewSnapshotOccupancyDeltaPerMinute(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.October, 3, 10, 5, 0, 0, time.UTC)

	measurement := func(pointID string, window time.Duration, countIn, countOut int32) domain.Measurement {
		m, err := domain.NewMeasurement(pointID, end.Add(-window), end, countIn, countOut, nil)
		if err != nil {
			t.Fatalf("NewMeasurement: %v", err)
		}

		return m
	}

	const (
		shortWindow = "00000000000000a1"
		longWindow  = "00000000000000a2"
	)

	measurements := []domain.Measurement{
		measurement(shortWindow, 10*time.Second, 3, 1),
		measurement(longWindow, 5*time.Minute, 20, 5),
	}

	mappings := []domain.ObservationPointMapping{
		{ObservationPointID: shortWindow, Anchor: domain.GraphAnchor{PointID: "p1"}},
		{ObservationPointID: longWindow, Anchor: domain.GraphAnchor{PointID: "p2"}},
	}

	states := map[domain.ObservationPointID]domain.ObservationPointState{
		shortWindow: domain.ObservationPointActive, longWindow: domain.ObservationPointActive,
	}

	snapshot, _, _, err := domain.NewSnapshot("event", end.Add(-5*time.Minute), end, measurements, mappings, states)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	got := map[string]float64{}
	for _, score := range snapshot.PointScores {
		got[score.PointID] = score.OccupancyDelta
	}

	want := map[string]float64{"p1": 12, "p2": 3}
	if !maps.Equal(got, want) {
		t.Errorf("occupancy delta per minute = %v, want %v", got, want)
	}
}

func TestNewSnapshotConfidence(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.October, 3, 10, 30, 0, 0, time.UTC)
	start := end.Add(-time.Minute)

	measurement := func(pointID string, windowEnd time.Time, countIn, countOut int32, detected *float64) domain.Measurement {
		m, err := domain.NewMeasurement(pointID, windowEnd.Add(-time.Minute), windowEnd, countIn, countOut, detected)
		if err != nil {
			t.Fatalf("NewMeasurement: %v", err)
		}

		return m
	}

	const (
		fresh       = "00000000000000a1"
		heldRecent  = "00000000000000a2"
		heldAtLimit = "00000000000000a3"
		expired     = "00000000000000a4"
		disabled    = "00000000000000a5"
		silent      = "00000000000000a6"
		composite   = "00000000000000a7"
		unknown     = "00000000000000a8"
		noPeople    = "00000000000000a9"
		offline10   = "00000000000000b1"
		offline16   = "00000000000000b2"
		offlineNow  = "00000000000000b3"
	)

	measurements := []domain.Measurement{
		measurement(fresh, end, 6, 2, new(3.0)),
		measurement(heldRecent, end.Add(-11*time.Minute), 1, 1, new(9.0)),
		measurement(heldRecent, end.Add(-10*time.Minute), 4, 2, new(5.0)),
		measurement(heldAtLimit, end.Add(-domain.MaxHold), 8, 0, nil),
		measurement(expired, end.Add(-domain.MaxHold-time.Microsecond), 7, 7, new(1.0)),
		measurement(disabled, end, 5, 5, new(2.0)),
		measurement(composite, end.Add(-3*time.Minute), 2, 1, new(1.0)),
		measurement(unknown, end, 9, 9, new(9.0)),
		measurement(noPeople, end, 3, 1, nil),
		measurement(offline10, end.Add(-10*time.Minute), 6, 1, new(2.0)),
		measurement(offline16, end.Add(-16*time.Minute), 6, 1, new(2.0)),
		measurement(offlineNow, end, 4, 0, new(7.0)),
	}

	mappings := []domain.ObservationPointMapping{
		{ObservationPointID: fresh, Anchor: domain.GraphAnchor{RouteID: "r-ok"}},
		{ObservationPointID: heldRecent, Anchor: domain.GraphAnchor{RouteID: "r-held"}},
		{ObservationPointID: heldAtLimit, Anchor: domain.GraphAnchor{PointID: "p-held-at-limit"}},
		{ObservationPointID: expired, Anchor: domain.GraphAnchor{RouteID: "r-expired"}},
		{ObservationPointID: disabled, Anchor: domain.GraphAnchor{PointID: "p-disabled"}},
		{ObservationPointID: silent, Anchor: domain.GraphAnchor{PointID: "p-silent"}},
		{ObservationPointID: fresh, Anchor: domain.GraphAnchor{RouteID: "r-composite"}},
		{ObservationPointID: composite, Anchor: domain.GraphAnchor{RouteID: "r-composite"}},
		{ObservationPointID: fresh, Anchor: domain.GraphAnchor{RouteID: "r-partly-invalid"}},
		{ObservationPointID: silent, Anchor: domain.GraphAnchor{RouteID: "r-partly-invalid"}},
		{ObservationPointID: unknown, Anchor: domain.GraphAnchor{RouteID: "r-unknown"}},
		{ObservationPointID: noPeople, Anchor: domain.GraphAnchor{PointID: "p-no-people"}},
		{ObservationPointID: offline10, Anchor: domain.GraphAnchor{PointID: "p-offline-10m"}},
		{ObservationPointID: offline16, Anchor: domain.GraphAnchor{PointID: "p-offline-16m"}},
		{ObservationPointID: offlineNow, Anchor: domain.GraphAnchor{RouteID: "r-offline-fresh"}},
	}

	active := domain.ObservationPointActive
	offline := domain.ObservationPointOffline
	states := map[domain.ObservationPointID]domain.ObservationPointState{
		fresh: active, heldRecent: active, heldAtLimit: active, expired: active, disabled: domain.ObservationPointDisabled,
		silent: active, composite: active, noPeople: active, offline10: offline, offline16: offline, offlineNow: offline,
	}

	snapshot, gaps, _, err := domain.NewSnapshot("event", start, end, measurements, mappings, states)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	routes := func(scores []domain.RouteScore) map[string]string {
		got := map[string]string{}
		for _, score := range scores {
			got[score.RouteID] = fmt.Sprintf("%v/%v/%v", score.Forward, score.Backward, deref(score.StagnationScore))
		}

		return got
	}

	points := func(scores []domain.PointScore) map[string]string {
		got := map[string]string{}
		for _, score := range scores {
			got[score.PointID] = fmt.Sprintf("%v/%v", deref(score.PeopleScore), score.OccupancyDelta)
		}

		return got
	}

	if got, want := routes(snapshot.RouteScores), map[string]string{"r-offline-fresh": "4/0/7", "r-ok": "6/2/3"}; !maps.Equal(got, want) {
		t.Errorf("OK route scores (forward/backward/stagnation) = %v, want %v", got, want)
	}

	if got, want := points(snapshot.PointScores), map[string]string{"p-no-people": "<nil>/2"}; !maps.Equal(got, want) {
		t.Errorf("OK point scores (people/occupancy delta) = %v, want %v", got, want)
	}

	wantHeldRoutes := map[string]string{"r-held": "4/2/5", "r-composite": "8/3/4"}
	if got := routes(gaps.HeldRouteScores); !maps.Equal(got, wantHeldRoutes) {
		t.Errorf("held route scores (forward/backward/stagnation) = %v, want %v", got, wantHeldRoutes)
	}

	if got, want := points(gaps.HeldPointScores), map[string]string{"p-held-at-limit": "<nil>/8", "p-offline-10m": "2/5"}; !maps.Equal(got, want) {
		t.Errorf("held point scores (people/occupancy delta) = %v, want %v", got, want)
	}

	if got, want := gaps.InvalidRouteIDs, []string{"r-expired", "r-partly-invalid"}; !slices.Equal(got, want) {
		t.Errorf("invalid routes = %v, want %v", got, want)
	}

	if got, want := gaps.InvalidPointIDs, []string{"p-disabled", "p-offline-16m", "p-silent"}; !slices.Equal(got, want) {
		t.Errorf("invalid points = %v, want %v", got, want)
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

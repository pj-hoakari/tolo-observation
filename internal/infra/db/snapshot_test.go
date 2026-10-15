package db

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

func saveRouteSnapshot(
	t *testing.T,
	repo *PostgresSnapshotRepository,
	eventID string,
	windowEnd time.Time,
	stagnations map[string]*float64,
) {
	t.Helper()

	snapshot := domain.Snapshot{
		ID:          fmt.Sprintf("%s-%d", eventID, windowEnd.UnixMicro()),
		EventID:     eventID,
		WindowStart: windowEnd.Add(-time.Minute),
		WindowEnd:   windowEnd,
	}

	for routeID, stagnation := range stagnations {
		snapshot.RouteScores = append(snapshot.RouteScores, domain.RouteScore{
			RouteID: routeID, Forward: 1, Backward: 1, StagnationScore: stagnation, Exhaustive: true,
		})
	}

	if err := repo.SaveSnapshot(context.Background(), "tenant-arc", snapshot); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
}

func TestPostgresSnapshotRepositorySummarizeArcHistory(t *testing.T) {
	repo := NewPostgresSnapshotRepository(testDB)
	ctx := context.Background()
	since := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

	saveRouteSnapshot(t, repo, "event-arc", since, map[string]*float64{"r1": new(1000.0)})

	for i := 1; i <= 10; i++ {
		stagnations := map[string]*float64{"r1": new(float64(i))}
		if i%2 == 0 {
			stagnations["r2"] = new(float64(i * 10))
		}

		if i == 1 {
			stagnations["r3"] = new(0.0)
		}

		saveRouteSnapshot(t, repo, "event-arc", since.Add(time.Duration(i)*time.Hour), stagnations)
	}

	saveRouteSnapshot(t, repo, "event-arc", since.Add(11*time.Hour), map[string]*float64{"r1": nil, "r2": nil, "r3": nil})
	saveRouteSnapshot(t, repo, "event-arc-other", since.Add(5*time.Hour), map[string]*float64{"r1": new(500.0)})

	got, err := repo.SummarizeArcHistory(ctx, "event-arc", since)
	if err != nil {
		t.Fatalf("SummarizeArcHistory: %v", err)
	}

	want := []domain.ArcHistory{
		{
			RouteID: "r1", SampleCount: 10, P90Stagnation: 9, MeanStagnation: 5.5,
			FirstStart: since.Add(time.Hour - time.Minute), LastEnd: since.Add(10 * time.Hour),
		},
		{
			RouteID: "r2", SampleCount: 5, P90Stagnation: 100, MeanStagnation: 60,
			FirstStart: since.Add(2*time.Hour - time.Minute), LastEnd: since.Add(10 * time.Hour),
		},
		{
			RouteID: "r3", SampleCount: 1, P90Stagnation: 0, MeanStagnation: 0,
			FirstStart: since.Add(time.Hour - time.Minute), LastEnd: since.Add(time.Hour),
		},
	}

	if len(got) != len(want) {
		t.Fatalf("SummarizeArcHistory = %+v, want %+v", got, want)
	}

	for i := range want {
		g, w := got[i], want[i]
		if g.RouteID != w.RouteID || g.SampleCount != w.SampleCount ||
			math.Abs(g.P90Stagnation-w.P90Stagnation) > 0.01*w.P90Stagnation || math.Abs(g.MeanStagnation-w.MeanStagnation) > 1e-9 ||
			!g.FirstStart.Equal(w.FirstStart) || !g.LastEnd.Equal(w.LastEnd) {
			t.Errorf("SummarizeArcHistory[%d] = %+v, want %+v", i, g, w)
		}
	}

	earliest, err := repo.EarliestSnapshotStart(ctx, "event-arc")
	if err != nil {
		t.Fatalf("EarliestSnapshotStart: %v", err)
	}

	if want := since.Add(-time.Minute); !earliest.Equal(want) {
		t.Errorf("EarliestSnapshotStart = %v, want %v", earliest, want)
	}

	if _, err := repo.EarliestSnapshotStart(ctx, "event-arc-missing"); !errors.Is(err, repository.ErrSnapshotNotFound) {
		t.Errorf("EarliestSnapshotStart without snapshots error = %v, want %v", err, repository.ErrSnapshotNotFound)
	}
}

func TestPostgresSnapshotRepositorySummarizeArcHistoryWithinOneHour(t *testing.T) {
	repo := NewPostgresSnapshotRepository(testDB)
	since := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

	for _, minute := range []int{10, 20, 30} {
		saveRouteSnapshot(t, repo, "event-arc-hour", since.Add(time.Duration(minute)*time.Minute), map[string]*float64{"r1": new(4.0)})
	}

	saveRouteSnapshot(t, repo, "event-arc-hour", since.Add(40*time.Minute), map[string]*float64{"r1": new(8.0)})

	got, err := repo.SummarizeArcHistory(context.Background(), "event-arc-hour", since)
	if err != nil {
		t.Fatalf("SummarizeArcHistory: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("SummarizeArcHistory = %+v, want one route", got)
	}

	g := got[0]
	if g.SampleCount != 4 || math.Abs(g.MeanStagnation-5) > 1e-9 || math.Abs(g.P90Stagnation-8) > 0.08 ||
		!g.FirstStart.Equal(since.Add(9*time.Minute)) || !g.LastEnd.Equal(since.Add(40*time.Minute)) {
		t.Errorf("SummarizeArcHistory = %+v, want 4 samples, mean 5, p90 8, from 00:09 to 00:40", g)
	}
}

func TestPostgresSnapshotRepositorySummarizeArcHistoryFlowSums(t *testing.T) {
	repo := NewPostgresSnapshotRepository(testDB)
	since := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

	samples := []struct {
		windowEnd         time.Time
		forward, backward float64
		stagnation        float64
		sameSensorFlow    bool
	}{
		{since.Add(10 * time.Minute), 3, 1, 5, true},
		{since.Add(20 * time.Minute), 2, 4, 5, false},
		{since.Add(90 * time.Minute), 0.5, 0, 2, true},
	}

	for _, sample := range samples {
		err := repo.SaveSnapshot(context.Background(), "tenant-arc", domain.Snapshot{
			ID:          fmt.Sprintf("event-arc-flow-%d", sample.windowEnd.UnixMicro()),
			EventID:     "event-arc-flow",
			WindowStart: sample.windowEnd.Add(-time.Minute),
			WindowEnd:   sample.windowEnd,
			RouteScores: []domain.RouteScore{{
				RouteID: "r1", Forward: sample.forward, Backward: sample.backward,
				StagnationScore: new(sample.stagnation), Exhaustive: true, SameSensorFlow: sample.sameSensorFlow,
			}},
		})
		if err != nil {
			t.Fatalf("SaveSnapshot: %v", err)
		}
	}

	got, err := repo.SummarizeArcHistory(context.Background(), "event-arc-flow", since)
	if err != nil {
		t.Fatalf("SummarizeArcHistory: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("SummarizeArcHistory = %+v, want one route", got)
	}

	g := got[0]
	if g.SampleCount != 3 || math.Abs(g.MeanStagnation-4) > 1e-9 {
		t.Errorf("SummarizeArcHistory = %+v, want 3 samples with mean stagnation 4", g)
	}

	want := domain.FlowRegression{SampleCount: 3, StagnationSum: 12, FlowSum: 10.5, FlowSquareSum: 52.25, FlowStagnationSum: 51}
	if !regressionEqual(g.Flow, want) {
		t.Errorf("SummarizeArcHistory flow = %+v, want %+v", g.Flow, want)
	}

	want = domain.FlowRegression{SampleCount: 2, StagnationSum: 7, FlowSum: 4.5, FlowSquareSum: 16.25, FlowStagnationSum: 21}
	if !regressionEqual(g.SameSensorFlow, want) {
		t.Errorf("SummarizeArcHistory same sensor flow = %+v, want %+v", g.SameSensorFlow, want)
	}
}

func regressionEqual(a, b domain.FlowRegression) bool {
	return a.SampleCount == b.SampleCount && math.Abs(a.StagnationSum-b.StagnationSum) < 1e-9 &&
		math.Abs(a.FlowSum-b.FlowSum) < 1e-9 && math.Abs(a.FlowSquareSum-b.FlowSquareSum) < 1e-9 &&
		math.Abs(a.FlowStagnationSum-b.FlowStagnationSum) < 1e-9
}

func TestPostgresSnapshotRepositorySummarizeArcHistoryAfterSensorSetChange(t *testing.T) {
	repo := NewPostgresSnapshotRepository(testDB)
	ctx := context.Background()
	since := time.Date(2026, time.October, 2, 0, 0, 0, 0, time.UTC)

	for _, minute := range []int{30, 70, 130} {
		saveRouteSnapshot(t, repo, "event-arc-change", since.Add(time.Duration(minute)*time.Minute),
			map[string]*float64{"r1": new(4.0), "r2": new(4.0)})
	}

	changed := []domain.SensorSet{
		{Anchor: domain.GraphAnchor{RouteID: "r1"}, ObservationPointIDs: []domain.ObservationPointID{"a", "b"}, ChangedAt: since.Add(65 * time.Minute)},
		{Anchor: domain.GraphAnchor{RouteID: "r2"}, ObservationPointIDs: []domain.ObservationPointID{"c"}},
		{Anchor: domain.GraphAnchor{PointID: "p1"}, ObservationPointIDs: []domain.ObservationPointID{"d"}, ChangedAt: since},
	}

	if err := repo.SaveSensorSets(ctx, "event-arc-change", changed); err != nil {
		t.Fatalf("SaveSensorSets: %v", err)
	}

	listed, err := repo.ListSensorSets(ctx, "event-arc-change")
	if err != nil {
		t.Fatalf("ListSensorSets: %v", err)
	}

	if len(listed) != len(changed) {
		t.Fatalf("ListSensorSets = %v, want %v", listed, changed)
	}

	for i, set := range listed {
		if set.Anchor != changed[i].Anchor || !slices.Equal(set.ObservationPointIDs, changed[i].ObservationPointIDs) ||
			!set.ChangedAt.Equal(changed[i].ChangedAt) {
			t.Errorf("ListSensorSets[%d] = %v, want %v", i, set, changed[i])
		}
	}

	got, err := repo.SummarizeArcHistory(ctx, "event-arc-change", since)
	if err != nil {
		t.Fatalf("SummarizeArcHistory: %v", err)
	}

	counts := map[string]int{}
	for _, history := range got {
		counts[history.RouteID] = history.SampleCount
	}

	if counts["r1"] != 2 || counts["r2"] != 3 {
		t.Errorf("sample counts = %v, want r1 without the hour started before its change and r2 untouched", counts)
	}
}

func TestPostgresSnapshotRepositoryKeepsMissingAnchorsOutOfHistory(t *testing.T) {
	repo := NewPostgresSnapshotRepository(testDB)
	ctx := context.Background()
	windowEnd := time.Date(2026, time.October, 3, 0, 0, 0, 0, time.UTC)

	snapshot := domain.Snapshot{
		ID:          "snapshot-missing",
		EventID:     "event-missing",
		WindowStart: windowEnd.Add(-time.Minute),
		WindowEnd:   windowEnd,
		PointScores: []domain.PointScore{{PointID: "p1", PeopleScore: new(2.0), OccupancyDelta: 1, Exhaustive: true}},
		RouteScores: []domain.RouteScore{{RouteID: "r1", Forward: 1, Backward: 1, StagnationScore: new(3.0), Exhaustive: true}},
		Missing:     domain.MissingAnchors{PointIDs: []string{"p2"}, RouteIDs: []string{"r2"}},
	}

	if err := repo.SaveSnapshot(ctx, "tenant-missing", snapshot); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	anchors := func(snapshot domain.Snapshot) string {
		got := ""
		for _, score := range snapshot.RouteScores {
			got += score.RouteID + " "
		}

		for _, score := range snapshot.PointScores {
			got += score.PointID + " "
		}

		return got
	}

	latest, err := repo.LatestSnapshot(ctx, "event-missing")
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}

	if !slices.Equal(latest.Missing.PointIDs, []string{"p2"}) || !slices.Equal(latest.Missing.RouteIDs, []string{"r2"}) {
		t.Errorf("LatestSnapshot missing = %+v, want points [p2] and routes [r2]", latest.Missing)
	}

	history, err := repo.ListSnapshotsEndingAfter(ctx, "event-missing", windowEnd.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListSnapshotsEndingAfter: %v", err)
	}

	if len(history) != 1 || anchors(history[0]) != "r1 p1 " {
		t.Errorf("ListSnapshotsEndingAfter = %+v, want scores of r1 and p1 only", history)
	}

	arcs, err := repo.SummarizeArcHistory(ctx, "event-missing", windowEnd.Add(-time.Hour))
	if err != nil {
		t.Fatalf("SummarizeArcHistory: %v", err)
	}

	if len(arcs) != 1 || arcs[0].RouteID != "r1" {
		t.Errorf("SummarizeArcHistory = %+v, want r1 only", arcs)
	}
}

func TestPostgresSnapshotRepositoryDecodesSnapshotsWithoutMissingAnchors(t *testing.T) {
	ctx := context.Background()
	windowEnd := time.Date(2026, time.October, 4, 0, 0, 0, 0, time.UTC)

	if _, err := testDB.ExecContext(ctx, `
		INSERT INTO snapshots (snapshot_id, tenant_public_id, event_id, window_start, window_end, scores)
		VALUES ('snapshot-legacy', 'tenant-legacy', 'event-legacy', $1, $2, $3)`,
		windowEnd.Add(-time.Minute), windowEnd,
		`{"points":[{"point_id":"p1","people_score":null,"occupancy_delta":1,"exhaustive":true}],"routes":[]}`,
	); err != nil {
		t.Fatalf("insert legacy snapshot: %v", err)
	}

	got, err := NewPostgresSnapshotRepository(testDB).LatestSnapshot(ctx, "event-legacy")
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}

	if len(got.PointScores) != 1 || got.PointScores[0].PointID != "p1" ||
		len(got.Missing.PointIDs)+len(got.Missing.RouteIDs) != 0 {
		t.Errorf("LatestSnapshot = %+v, want p1 and no missing anchors", got)
	}
}

package db

import (
	"context"
	"errors"
	"fmt"
	"math"
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

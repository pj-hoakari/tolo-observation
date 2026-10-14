package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestTrackSensorSets(t *testing.T) {
	at := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	earlier := at.Add(-time.Hour)
	r1 := domain.GraphAnchor{RouteID: "r1"}
	r2 := domain.GraphAnchor{RouteID: "r2"}
	p1 := domain.GraphAnchor{PointID: "p1"}

	recorded := []domain.SensorSet{
		{Anchor: r1, ObservationPointIDs: []domain.ObservationPointID{"a"}, ChangedAt: earlier},
		{Anchor: r2, ObservationPointIDs: []domain.ObservationPointID{"b"}},
		{Anchor: p1, ObservationPointIDs: []domain.ObservationPointID{"c"}},
	}
	composed := []domain.SensorSet{
		{Anchor: r1, ObservationPointIDs: []domain.ObservationPointID{"a", "d"}},
		{Anchor: r2, ObservationPointIDs: []domain.ObservationPointID{"b"}},
		{Anchor: domain.GraphAnchor{PointID: "p2"}, ObservationPointIDs: []domain.ObservationPointID{"e"}},
	}

	tracked, updated := domain.TrackSensorSets(recorded, composed, at)

	changedAt := func(sets []domain.SensorSet) map[domain.GraphAnchor]time.Time {
		got := map[domain.GraphAnchor]time.Time{}
		for _, set := range sets {
			got[set.Anchor] = set.ChangedAt
		}

		return got
	}

	wantUpdated := map[domain.GraphAnchor]time.Time{r1: at, {PointID: "p2"}: {}}
	if got := changedAt(updated); len(got) != len(wantUpdated) || got[r1] != at || !got[domain.GraphAnchor{PointID: "p2"}].IsZero() {
		t.Errorf("updated = %v, want r1 changed now and p2 recorded without a change", updated)
	}

	if got := changedAt(domain.SensorSetsChangedAfter(tracked, earlier)); len(got) != 1 || got[r1] != at {
		t.Errorf("changed after %v = %v, want only r1", earlier, got)
	}

	r1Index := slices.IndexFunc(tracked, func(set domain.SensorSet) bool { return set.Anchor == r1 })
	if len(tracked) != 4 || r1Index < 0 || !slices.Equal(tracked[r1Index].ObservationPointIDs, []domain.ObservationPointID{"a", "d"}) {
		t.Errorf("tracked = %v, want r1 with its new points and the other anchors kept", tracked)
	}
}

func TestHistorySinceSensorSetChanges(t *testing.T) {
	at := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	snapshot := func(windowEnd time.Time) domain.Snapshot {
		return domain.Snapshot{
			ID: "s", EventID: "e", WindowStart: windowEnd.Add(-time.Minute), WindowEnd: windowEnd,
			PointScores: []domain.PointScore{{PointID: "p1"}},
			RouteScores: []domain.RouteScore{{RouteID: "r1"}, {RouteID: "r2"}},
		}
	}
	history := []domain.Snapshot{snapshot(at.Add(-time.Second)), snapshot(at)}
	sets := []domain.SensorSet{
		{Anchor: domain.GraphAnchor{RouteID: "r1"}, ChangedAt: at},
		{Anchor: domain.GraphAnchor{PointID: "p1"}, ChangedAt: at},
		{Anchor: domain.GraphAnchor{RouteID: "r2"}},
	}

	got := domain.HistorySinceSensorSetChanges(history, sets)

	if len(got[0].RouteScores) != 1 || got[0].RouteScores[0].RouteID != "r2" || len(got[0].PointScores) != 0 {
		t.Errorf("before the change = %+v, want only r2", got[0])
	}

	if len(got[1].RouteScores) != 2 || len(got[1].PointScores) != 1 {
		t.Errorf("at the change = %+v, want every anchor", got[1])
	}

	if len(history[0].RouteScores) != 2 {
		t.Errorf("history was modified: %+v", history[0])
	}
}

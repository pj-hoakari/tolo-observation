package domain

import (
	"maps"
	"slices"
	"time"
)

type SensorSet struct {
	Anchor              GraphAnchor
	ObservationPointIDs []ObservationPointID
	ChangedAt           time.Time
}

func TrackSensorSets(recorded, composed []SensorSet, at time.Time) (tracked, updated []SensorSet) {
	byAnchor := make(map[GraphAnchor]SensorSet, len(recorded))
	for _, set := range recorded {
		byAnchor[set.Anchor] = set
	}

	for _, set := range composed {
		previous, ok := byAnchor[set.Anchor]

		switch {
		case !ok:
			set.ChangedAt = time.Time{}
		case slices.Equal(previous.ObservationPointIDs, set.ObservationPointIDs):
			continue
		default:
			set.ChangedAt = at
		}

		byAnchor[set.Anchor] = set
		updated = append(updated, set)
	}

	tracked = slices.SortedFunc(maps.Values(byAnchor), func(a, b SensorSet) int { return compareAnchors(a.Anchor, b.Anchor) })

	return tracked, updated
}

func SensorSetsChangedAfter(sets []SensorSet, after time.Time) []SensorSet {
	var changed []SensorSet

	for _, set := range sets {
		if set.ChangedAt.After(after) {
			changed = append(changed, set)
		}
	}

	return changed
}

func HistorySinceSensorSetChanges(history []Snapshot, sets []SensorSet) []Snapshot {
	changedAt := map[GraphAnchor]time.Time{}

	for _, set := range sets {
		if !set.ChangedAt.IsZero() {
			changedAt[set.Anchor] = set.ChangedAt
		}
	}

	trimmed := make([]Snapshot, 0, len(history))

	for _, snapshot := range history {
		predates := func(anchor GraphAnchor) bool {
			at, ok := changedAt[anchor]

			return ok && snapshot.WindowEnd.Before(at)
		}

		snapshot.PointScores = slices.DeleteFunc(slices.Clone(snapshot.PointScores), func(score PointScore) bool {
			return predates(GraphAnchor{PointID: score.PointID, RouteID: ""})
		})
		snapshot.RouteScores = slices.DeleteFunc(slices.Clone(snapshot.RouteScores), func(score RouteScore) bool {
			return predates(GraphAnchor{PointID: "", RouteID: score.RouteID})
		})

		trimmed = append(trimmed, snapshot)
	}

	return trimmed
}

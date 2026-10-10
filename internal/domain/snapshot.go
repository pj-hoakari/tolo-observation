package domain

import (
	"cmp"
	"slices"
	"time"
)

type PointType int

const (
	PointTypeUnspecified PointType = iota
	PointTypeGoal
	PointTypeGoalTransitMixed
	PointTypeTransitOnly
)

type Point struct {
	ID             string
	Type           PointType
	IsBoundary     bool
	BoundaryActive bool
}

type Route struct {
	ID           string
	FromPointID  string
	ToPointID    string
	OneWay       bool
	CapacityHint *float64
}

type Graph struct {
	EventID    string
	RevisionID string
	Points     []Point
	Routes     []Route
}

type GraphAnchor struct {
	PointID string
	RouteID string
}

type ObservationPointMapping struct {
	ObservationPointID ObservationPointID
	Anchor             GraphAnchor
}

type PointScore struct {
	PointID        string
	PeopleScore    float64
	OccupancyDelta float64
	Exhaustive     bool
}

type RouteScore struct {
	RouteID         string
	Forward         float64
	Backward        float64
	StagnationScore float64
	Exhaustive      bool
}

type Snapshot struct {
	ID          string
	EventID     string
	WindowStart time.Time
	WindowEnd   time.Time
	PointScores []PointScore
	RouteScores []RouteScore
}

func NewSnapshot(
	eventID string,
	windowStart, windowEnd time.Time,
	measurements []Measurement,
	mappings []ObservationPointMapping,
) (Snapshot, error) {
	id, err := newPublicID()
	if err != nil {
		return Snapshot{}, err
	}

	latest := make(map[ObservationPointID]Measurement, len(measurements))
	detected := map[ObservationPointID][]float64{}

	for _, measurement := range measurements {
		current, ok := latest[measurement.ObservationPointID]
		if !ok || measurement.WindowEnd.After(current.WindowEnd) {
			latest[measurement.ObservationPointID] = measurement
		}

		if measurement.MeanDetectedPeople != nil {
			detected[measurement.ObservationPointID] = append(detected[measurement.ObservationPointID], *measurement.MeanDetectedPeople)
		}
	}

	points := map[string]*PointScore{}
	routes := map[string]*RouteScore{}

	for _, mapping := range mappings {
		measurement, ok := latest[mapping.ObservationPointID]
		if !ok {
			continue
		}

		switch {
		case mapping.Anchor.RouteID != "":
			score, ok := routes[mapping.Anchor.RouteID]
			if !ok {
				score = &RouteScore{RouteID: mapping.Anchor.RouteID, Forward: 0, Backward: 0, StagnationScore: 0, Exhaustive: true}
				routes[mapping.Anchor.RouteID] = score
			}

			score.Forward += measurement.RateIn()
			score.Backward += measurement.RateOut()
			score.StagnationScore += mean(detected[mapping.ObservationPointID])
		case mapping.Anchor.PointID != "":
			score, ok := points[mapping.Anchor.PointID]
			if !ok {
				score = &PointScore{PointID: mapping.Anchor.PointID, PeopleScore: 0, OccupancyDelta: 0, Exhaustive: true}
				points[mapping.Anchor.PointID] = score
			}

			score.PeopleScore += mean(detected[mapping.ObservationPointID])
			score.OccupancyDelta += float64(measurement.CountIn - measurement.CountOut)
		}
	}

	return Snapshot{
		ID:          id,
		EventID:     eventID,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		PointScores: sortedScores(points, func(score PointScore) string { return score.PointID }),
		RouteScores: sortedScores(routes, func(score RouteScore) string { return score.RouteID }),
	}, nil
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	var sum float64
	for _, value := range values {
		sum += value
	}

	return sum / float64(len(values))
}

func sortedScores[S any](scores map[string]*S, key func(S) string) []S {
	sorted := make([]S, 0, len(scores))
	for _, score := range scores {
		sorted = append(sorted, *score)
	}

	slices.SortFunc(sorted, func(a, b S) int { return cmp.Compare(key(a), key(b)) })

	return sorted
}

type OptimizationOutcome struct {
	SnapshotID         string
	Verdict            string
	DetectionState     []byte
	OptimizationResult []byte
}

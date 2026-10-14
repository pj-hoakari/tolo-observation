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
	ID       string
	Type     PointType
	Boundary *Boundary
}

type BoundaryDirection int

const (
	BoundaryDirectionUnspecified BoundaryDirection = iota
	BoundaryDirectionEntry
	BoundaryDirectionExit
	BoundaryDirectionEntryAndExit
)

type Boundary struct {
	Direction BoundaryDirection
	Active    bool
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
	PeopleScore    *float64
	OccupancyDelta float64
	Exhaustive     bool
}

type RouteScore struct {
	RouteID         string
	Forward         float64
	Backward        float64
	StagnationScore *float64
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

type ArcHistory struct {
	RouteID           string
	SampleCount       int
	P90Stagnation     float64
	MeanStagnation    float64
	FirstStart        time.Time
	LastEnd           time.Time
	FlowSum           float64
	FlowSquareSum     float64
	FlowStagnationSum float64
}

const minFlowSensitivitySamples = 12

func (h ArcHistory) FlowSensitivityEta() (float64, bool) {
	if h.SampleCount < minFlowSensitivitySamples {
		return 0, false
	}

	n := float64(h.SampleCount)
	flowVariance := n*h.FlowSquareSum - h.FlowSum*h.FlowSum

	if flowVariance <= 1e-9*n*h.FlowSquareSum {
		return 0, false
	}

	covariance := n*h.FlowStagnationSum - h.FlowSum*n*h.MeanStagnation
	eta := -covariance / flowVariance

	if eta <= 0 {
		return 0, false
	}

	return eta, true
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
				score = &RouteScore{RouteID: mapping.Anchor.RouteID, Forward: 0, Backward: 0, StagnationScore: nil, Exhaustive: true}
				routes[mapping.Anchor.RouteID] = score
			}

			score.Forward += measurement.RateIn()
			score.Backward += measurement.RateOut()

			score.StagnationScore = addMean(score.StagnationScore, detected[mapping.ObservationPointID])
		case mapping.Anchor.PointID != "":
			score, ok := points[mapping.Anchor.PointID]
			if !ok {
				score = &PointScore{PointID: mapping.Anchor.PointID, PeopleScore: nil, OccupancyDelta: 0, Exhaustive: true}
				points[mapping.Anchor.PointID] = score
			}

			score.PeopleScore = addMean(score.PeopleScore, detected[mapping.ObservationPointID])
			score.OccupancyDelta += measurement.RateIn() - measurement.RateOut()
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

func addMean(total *float64, values []float64) *float64 {
	if len(values) == 0 {
		return total
	}

	var sum float64
	for _, value := range values {
		sum += value
	}

	result := sum / float64(len(values))
	if total != nil {
		result += *total
	}

	return &result
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

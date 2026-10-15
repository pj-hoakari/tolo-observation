package domain

import (
	"cmp"
	"maps"
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

type MeasurementMethod int

const (
	MeasurementMethodCamera MeasurementMethod = iota
	MeasurementMethodQR
)

type ObservationPointMapping struct {
	ObservationPointID ObservationPointID
	Anchor             GraphAnchor
	Method             MeasurementMethod
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
	ScalarFlow      *float64
	StagnationScore *float64
	Exhaustive      bool
	SameSensorFlow  bool
}

type Snapshot struct {
	ID          string
	EventID     string
	WindowStart time.Time
	WindowEnd   time.Time
	PointScores []PointScore
	RouteScores []RouteScore
	Missing     MissingAnchors
}

type MissingAnchors struct {
	PointIDs []string
	RouteIDs []string
}

type ArcHistory struct {
	RouteID        string
	SampleCount    int
	P90Stagnation  float64
	MeanStagnation float64
	FirstStart     time.Time
	LastEnd        time.Time
	Flow           FlowRegression
	SameSensorFlow FlowRegression
}

type FlowRegression struct {
	SampleCount       int
	StagnationSum     float64
	FlowSum           float64
	FlowSquareSum     float64
	FlowStagnationSum float64
}

const minFlowSensitivitySamples = 12

func (h ArcHistory) FlowSensitivityEta() (float64, bool) {
	if h.SameSensorFlow.SampleCount >= minFlowSensitivitySamples {
		return h.SameSensorFlow.eta()
	}

	return h.Flow.eta()
}

func (r FlowRegression) eta() (float64, bool) {
	if r.SampleCount < minFlowSensitivitySamples {
		return 0, false
	}

	n := float64(r.SampleCount)
	flowVariance := n*r.FlowSquareSum - r.FlowSum*r.FlowSum

	if flowVariance <= 1e-9*n*r.FlowSquareSum {
		return 0, false
	}

	covariance := n*r.FlowStagnationSum - r.FlowSum*r.StagnationSum
	eta := -covariance / flowVariance

	if eta <= 0 {
		return 0, false
	}

	return eta, true
}

const MaxHold = 15 * time.Minute

type Gaps struct {
	HeldPointScores []PointScore
	HeldRouteScores []RouteScore
	InvalidPointIDs []string
	InvalidRouteIDs []string
}

type ObservationPointState int

const (
	ObservationPointActive ObservationPointState = iota
	ObservationPointOffline
	ObservationPointDisabled
)

type confidence int

const (
	confidenceOK confidence = iota
	confidenceHold
	confidenceInvalid
)

type reading struct {
	latest   Measurement
	detected []float64
	countIn  int64
}

type anchorScore struct {
	confidence confidence
	rateIn     float64
	rateOut    float64
	detected   *float64
	sensors    []ObservationPointID
	qr         bool
}

func NewSnapshot(
	eventID string,
	windowStart, windowEnd time.Time,
	measurements []Measurement,
	mappings []ObservationPointMapping,
	states map[ObservationPointID]ObservationPointState,
) (Snapshot, Gaps, []SensorSet, error) {
	id, err := newPublicID()
	if err != nil {
		return Snapshot{}, Gaps{}, nil, err
	}

	qr := map[ObservationPointID]bool{}

	for _, mapping := range mappings {
		if mapping.Method == MeasurementMethodQR {
			qr[mapping.ObservationPointID] = true
		}
	}

	readings := map[ObservationPointID]*reading{}

	for _, measurement := range measurements {
		state, known := states[measurement.ObservationPointID]
		if (!known || state == ObservationPointDisabled) && !qr[measurement.ObservationPointID] ||
			windowEnd.Sub(measurement.WindowEnd) > MaxHold {
			continue
		}

		current, ok := readings[measurement.ObservationPointID]
		if !ok {
			current = &reading{latest: measurement, detected: nil, countIn: 0}
			readings[measurement.ObservationPointID] = current
		}

		if measurement.WindowEnd.After(windowStart) {
			current.countIn += int64(measurement.CountIn)
		}

		if measurement.WindowEnd.After(current.latest.WindowEnd) {
			current.latest = measurement
		}

		if measurement.MeanDetectedPeople != nil && measurement.WindowEnd.After(windowStart) {
			current.detected = append(current.detected, *measurement.MeanDetectedPeople)
		}
	}

	anchors := map[GraphAnchor]*anchorScore{}

	windowMinutes := windowEnd.Sub(windowStart).Minutes()

	for _, mapping := range mappings {
		_, known := states[mapping.ObservationPointID]
		if !known && mapping.Method != MeasurementMethodQR {
			continue
		}

		score, ok := anchors[mapping.Anchor]
		if !ok {
			score = &anchorScore{confidence: confidenceOK, rateIn: 0, rateOut: 0, detected: nil, sensors: nil, qr: false}
			anchors[mapping.Anchor] = score
		}

		score.sensors = append(score.sensors, mapping.ObservationPointID)

		current, ok := readings[mapping.ObservationPointID]

		if mapping.Method == MeasurementMethodQR {
			score.qr = true

			if ok {
				score.rateIn += float64(current.countIn) / windowMinutes
			}

			continue
		}

		if !ok {
			score.confidence = confidenceInvalid

			continue
		}

		detected := current.detected
		if !current.latest.WindowEnd.After(windowStart) {
			score.confidence = max(score.confidence, confidenceHold)

			detected = nil
			if current.latest.MeanDetectedPeople != nil {
				detected = []float64{*current.latest.MeanDetectedPeople}
			}
		}

		score.rateIn += current.latest.RateIn()
		score.rateOut += current.latest.RateOut()
		score.detected = addMean(score.detected, detected)
	}

	snapshot := Snapshot{
		ID:          id,
		EventID:     eventID,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		PointScores: nil,
		RouteScores: nil,
		Missing:     MissingAnchors{PointIDs: nil, RouteIDs: nil},
	}

	var (
		gaps    Gaps
		sensors []SensorSet
	)

	for _, anchor := range slices.SortedFunc(maps.Keys(anchors), compareAnchors) {
		score := anchors[anchor]

		switch {
		case anchor.RouteID != "" && score.confidence == confidenceInvalid:
			gaps.InvalidRouteIDs = append(gaps.InvalidRouteIDs, anchor.RouteID)
			snapshot.Missing.RouteIDs = append(snapshot.Missing.RouteIDs, anchor.RouteID)
		case anchor.RouteID != "" && score.confidence == confidenceHold:
			gaps.HeldRouteScores = append(gaps.HeldRouteScores, score.route(anchor.RouteID))
			snapshot.Missing.RouteIDs = append(snapshot.Missing.RouteIDs, anchor.RouteID)
		case anchor.RouteID != "":
			snapshot.RouteScores = append(snapshot.RouteScores, score.route(anchor.RouteID))
			sensors = append(sensors, score.sensorSet(anchor))
		case anchor.PointID != "" && score.confidence == confidenceInvalid:
			gaps.InvalidPointIDs = append(gaps.InvalidPointIDs, anchor.PointID)
			snapshot.Missing.PointIDs = append(snapshot.Missing.PointIDs, anchor.PointID)
		case anchor.PointID != "" && score.confidence == confidenceHold:
			gaps.HeldPointScores = append(gaps.HeldPointScores, score.point(anchor.PointID))
			snapshot.Missing.PointIDs = append(snapshot.Missing.PointIDs, anchor.PointID)
		case anchor.PointID != "":
			snapshot.PointScores = append(snapshot.PointScores, score.point(anchor.PointID))
			sensors = append(sensors, score.sensorSet(anchor))
		}
	}

	return snapshot, gaps, sensors, nil
}

func compareAnchors(a, b GraphAnchor) int {
	return cmp.Or(cmp.Compare(a.RouteID, b.RouteID), cmp.Compare(a.PointID, b.PointID))
}

func (s anchorScore) route(routeID string) RouteScore {
	if s.qr {
		scalar := s.rateIn + s.rateOut

		return RouteScore{
			RouteID: routeID, Forward: 0, Backward: 0, ScalarFlow: &scalar, StagnationScore: s.detected, Exhaustive: false,
			SameSensorFlow: false,
		}
	}

	return RouteScore{
		RouteID: routeID, Forward: s.rateIn, Backward: s.rateOut, ScalarFlow: nil, StagnationScore: s.detected, Exhaustive: true,
		SameSensorFlow: s.detected != nil && len(s.distinctSensors()) == 1,
	}
}

func (s anchorScore) sensorSet(anchor GraphAnchor) SensorSet {
	return SensorSet{Anchor: anchor, ObservationPointIDs: s.distinctSensors(), ChangedAt: time.Time{}}
}

func (s anchorScore) distinctSensors() []ObservationPointID {
	return slices.Compact(slices.Sorted(slices.Values(s.sensors)))
}

func (s anchorScore) point(pointID string) PointScore {
	return PointScore{PointID: pointID, PeopleScore: s.detected, OccupancyDelta: s.rateIn - s.rateOut, Exhaustive: !s.qr}
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

type OptimizationOutcome struct {
	SnapshotID         string
	ServerTime         time.Time
	Verdict            string
	DetectionState     []byte
	OptimizationResult []byte
}

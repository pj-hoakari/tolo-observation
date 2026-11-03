package connect

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestFlowObservationsLeavesOccupancyUnsetWithoutDetectedPeople(t *testing.T) {
	people := 0.0
	snapshot := domain.Snapshot{
		ID:          "snapshot",
		EventID:     "event",
		WindowStart: time.Unix(0, 0),
		WindowEnd:   time.Unix(60, 0),
		PointScores: []domain.PointScore{
			{PointID: "p1", PeopleScore: &people, OccupancyDelta: 2, Exhaustive: true},
			{PointID: "p2", PeopleScore: nil, OccupancyDelta: 3, Exhaustive: true},
		},
		RouteScores: nil,
	}

	observations := flowObservations(snapshot, domain.Gaps{}, map[string]bool{"p1": true, "p2": true}, nil)

	got := map[string]string{}

	for _, occupancy := range observations.GetNodeOccupancies() {
		level := "unset"
		if occupancy.Occupancy != nil {
			level = fmt.Sprint(occupancy.GetOccupancy())
		}

		got[occupancy.GetNodeId()] = fmt.Sprintf("%s/%v", level, occupancy.GetOccupancyDelta())
	}

	want := map[string]string{"p1": "0/2", "p2": "unset/3"}
	if !maps.Equal(got, want) {
		t.Errorf("node occupancies (occupancy/delta) = %v, want %v", got, want)
	}
}

func TestNewOptimizeRequestSendsShortFallbackEta(t *testing.T) {
	request, err := newOptimizeRequest(application.OptimizeInput{})
	if err != nil {
		t.Fatalf("newOptimizeRequest() error = %v", err)
	}

	if got := request.GetConfig().GetFallbackEta(); got != 0.25 {
		t.Errorf("config fallback_eta = %v, want 0.25", got)
	}
}

func TestNewOptimizeRequestReadsDetectionStateSavedBeforeDemandExcessWasRemoved(t *testing.T) {
	saved := []byte(`{"cooldownUntil":"2026-11-03T00:00:00Z","arcDemandDigest":{"entries":[{"edgeId":"r1","demand":3}]},` +
		`"arcWatchStates":[{"edgeId":"r1","surgeBreached":true,"demandExcessBreached":true,"demandWatchSince":"2026-11-03T00:00:00Z"}]}`)

	request, err := newOptimizeRequest(application.OptimizeInput{
		Previous: &domain.OptimizationOutcome{DetectionState: saved},
	})
	if err != nil {
		t.Fatalf("newOptimizeRequest() error = %v", err)
	}

	state := request.GetDetectionState()
	if !state.GetCooldownUntil().AsTime().Equal(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) ||
		len(state.GetArcWatchStates()) != 1 || !state.GetArcWatchStates()[0].GetSurgeBreached() {
		t.Errorf("detection state = %v, want the cooldown and the surge watch kept", state)
	}
}

func TestFlowObservationsCarriesGapFlags(t *testing.T) {
	people := 4.0
	stagnation := 3.0
	snapshot := domain.Snapshot{ID: "snapshot", WindowStart: time.Unix(0, 0), WindowEnd: time.Unix(60, 0)}
	gaps := domain.Gaps{
		HeldPointScores: []domain.PointScore{{PointID: "p-held", PeopleScore: &people, OccupancyDelta: 2, Exhaustive: true}},
		HeldRouteScores: []domain.RouteScore{{RouteID: "r-held", Forward: 5, Backward: 1, StagnationScore: &stagnation, Exhaustive: true}},
		InvalidPointIDs: []string{"p-invalid"},
		InvalidRouteIDs: []string{"r-invalid"},
	}

	observations := flowObservations(snapshot, gaps,
		map[string]bool{"p-held": true, "p-invalid": true}, map[string]bool{"r-held": true, "r-invalid": true})

	got := map[string]string{}

	for _, occupancy := range observations.GetNodeOccupancies() {
		var delta any
		if occupancy.OccupancyDelta != nil {
			delta = occupancy.GetOccupancyDelta()
		}

		got[occupancy.GetNodeId()] = fmt.Sprintf("%v/%v/%v", occupancy.Occupancy != nil, delta, occupancy.GetConfidenceFlag())
	}

	for _, flow := range observations.GetArcFlows() {
		got[flow.GetEdgeId()+"/"+flow.GetDirection().String()] = fmt.Sprintf("%v/%v", flow.GetFlowRate(), flow.GetConfidenceFlag())
	}

	for _, stagnation := range observations.GetArcStagnations() {
		got[stagnation.GetEdgeId()+"/stagnation"] = fmt.Sprintf("%v/%v", stagnation.GetStagnation(), stagnation.GetConfidenceFlag())
	}

	want := map[string]string{
		"p-held":                          "true/<nil>/CONFIDENCE_FLAG_HOLD",
		"p-invalid":                       "false/0/CONFIDENCE_FLAG_INVALID",
		"r-held/FLOW_DIRECTION_A_TO_B":    "5/CONFIDENCE_FLAG_HOLD",
		"r-held/FLOW_DIRECTION_B_TO_A":    "1/CONFIDENCE_FLAG_HOLD",
		"r-held/stagnation":               "3/CONFIDENCE_FLAG_HOLD",
		"r-invalid/FLOW_DIRECTION_A_TO_B": "0/CONFIDENCE_FLAG_INVALID",
		"r-invalid/FLOW_DIRECTION_B_TO_A": "0/CONFIDENCE_FLAG_INVALID",
	}
	if !maps.Equal(got, want) {
		t.Errorf("observations = %v, want %v", got, want)
	}
}

func TestFlowRequestLeavesOutScoresThatAreNotExhaustive(t *testing.T) {
	end := time.Unix(60, 0)
	snapshot := domain.Snapshot{
		ID:          "snapshot",
		WindowStart: time.Unix(0, 0),
		WindowEnd:   end,
		PointScores: []domain.PointScore{
			{PointID: "p-camera", PeopleScore: nil, OccupancyDelta: 1, Exhaustive: true},
			{PointID: "p-qr", PeopleScore: nil, OccupancyDelta: 4, Exhaustive: false},
		},
		RouteScores: []domain.RouteScore{
			{RouteID: "r-camera", Forward: 2, Backward: 1, StagnationScore: nil, Exhaustive: true},
			{RouteID: "r-qr", Forward: 7, Backward: 0, StagnationScore: nil, Exhaustive: false},
		},
	}
	nodes := map[string]bool{"p-camera": true, "p-qr": true}
	edges := map[string]bool{"r-camera": true, "r-qr": true}

	observations := flowObservations(snapshot, domain.Gaps{}, nodes, edges)

	var got []string
	for _, occupancy := range observations.GetNodeOccupancies() {
		got = append(got, occupancy.GetNodeId())
	}

	for _, flow := range observations.GetArcFlows() {
		got = append(got, flow.GetEdgeId())
	}

	if want := []string{"p-camera", "r-camera", "r-camera"}; !slices.Equal(got, want) {
		t.Errorf("observed targets = %v, want only the exhaustive scores %v", got, want)
	}

	var series []string
	for _, arc := range flowHistoryDigest([]domain.Snapshot{snapshot}, nil, edges).GetWindowSeries() {
		series = append(series, arc.GetEdgeId())
	}

	if want := []string{"r-camera"}; !slices.Equal(series, want) {
		t.Errorf("history window series = %v, want only the exhaustive route %v", series, want)
	}
}

func TestNewOptimizeRequestSendsEachElementsMeasurementWindowAsItsResolution(t *testing.T) {
	end := time.Unix(3600, 0)

	request, err := newOptimizeRequest(application.OptimizeInput{
		Graph: domain.Graph{
			Points: []domain.Point{{ID: "p1"}, {ID: "p2"}},
			Routes: []domain.Route{{ID: "r1", FromPointID: "p1", ToPointID: "p2"}, {ID: "r2", FromPointID: "p2", ToPointID: "p1"}},
		},
		Snapshot: domain.Snapshot{
			WindowStart: end.Add(-time.Minute),
			WindowEnd:   end,
			Resolutions: map[domain.GraphAnchor]time.Duration{{RouteID: "r1"}: 30 * time.Second, {PointID: "p1"}: 10 * time.Second},
		},
	})
	if err != nil {
		t.Fatalf("newOptimizeRequest() error = %v", err)
	}

	got := map[string]int32{}
	for _, node := range request.GetGraph().GetNodes() {
		got["node:"+node.GetNodeId()] = node.GetTimeResolutionS()
	}

	for _, edge := range request.GetGraph().GetEdges() {
		got["edge:"+edge.GetEdgeId()] = edge.GetTimeResolutionS()
	}

	want := map[string]int32{"node:p1": 10, "node:p2": 60, "edge:r1": 30, "edge:r2": 60}
	if !maps.Equal(got, want) {
		t.Errorf("time_resolution_s = %v, want %v, with the cycle window for elements without a measurement", got, want)
	}
}

func TestFlowHistoryDigestSendsFlowStatsAfterFourHoursAndTheShareOfEstablishedEdges(t *testing.T) {
	start := time.Unix(0, 0)
	dwell := 0.5

	digest := flowHistoryDigest(nil, []domain.ArcHistory{
		{
			RouteID: "r1", P90Stagnation: 6, MeanStagnation: 3, FirstStart: start, LastEnd: start.Add(5 * time.Hour),
			FlowDwell: domain.FlowDwellSummary{FlowP90: 18, FreeFlowDwellMin: &dwell, Span: 5 * time.Hour},
		},
		{
			RouteID: "r2", P90Stagnation: 6, MeanStagnation: 3, FirstStart: start, LastEnd: start.Add(time.Hour),
			FlowDwell: domain.FlowDwellSummary{FlowP90: 18, FreeFlowDwellMin: &dwell, Span: time.Hour},
		},
	}, map[string]bool{"r1": true, "r2": true, "r3": true, "r4": true})

	stats := map[string]string{}
	for _, stat := range digest.GetArcStats() {
		stats[stat.GetEdgeId()] = fmt.Sprintf("%v/%v", stat.FlowP90 != nil, stat.FreeFlowDwellMin != nil)
	}

	want := map[string]string{"r1": "true/true", "r2": "false/false"}
	if !maps.Equal(stats, want) {
		t.Errorf("flow_p90/free_flow_dwell_min set = %v, want %v", stats, want)
	}

	if got := digest.GetCompleteness(); got != 0.25 {
		t.Errorf("completeness = %v, want 0.25, one established edge out of four in the graph", got)
	}
}

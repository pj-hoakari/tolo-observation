package connect

import (
	"fmt"
	"maps"
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

func TestFlowObservationsCarriesGapFlags(t *testing.T) {
	people := 4.0
	stagnation := 3.0
	snapshot := domain.Snapshot{ID: "snapshot", WindowStart: time.Unix(0, 0), WindowEnd: time.Unix(60, 0)}
	gaps := domain.Gaps{
		HeldPointScores: []domain.PointScore{{PointID: "p-held", PeopleScore: &people, OccupancyDelta: 2}},
		HeldRouteScores: []domain.RouteScore{{RouteID: "r-held", Forward: 5, Backward: 1, StagnationScore: &stagnation}},
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

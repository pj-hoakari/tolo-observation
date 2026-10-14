package connect

import (
	"maps"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

func TestFlowObservationsOmitsNodeOccupancyWithoutDetectedPeople(t *testing.T) {
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

	observations := flowObservations(snapshot, map[string]bool{"p1": true, "p2": true}, nil)

	got := map[string]float64{}
	for _, occupancy := range observations.GetNodeOccupancies() {
		got[occupancy.GetNodeId()] = occupancy.GetOccupancy()
	}

	if want := map[string]float64{"p1": 0}; !maps.Equal(got, want) {
		t.Errorf("node occupancies = %v, want %v without p2, which reported no detected people", got, want)
	}
}

package connect

import (
	"fmt"
	"maps"
	"testing"
	"time"

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

	observations := flowObservations(snapshot, map[string]bool{"p1": true, "p2": true}, nil)

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

package connect

import (
	"testing"

	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
)

func TestEveryFlowDiagnosticsEnumValueHasAnObservationCounterpart(t *testing.T) {
	pairs := []struct {
		flow        map[int32]string
		observation map[string]int32
	}{
		{flowv1.ObservationState_name, observationv1.FlowObservationState_value},
		{flowv1.HistoryTier_name, observationv1.FlowHistoryTier_value},
		{flowv1.Judgement_name, observationv1.FlowJudgement_value},
		{flowv1.WarningCode_name, observationv1.FlowWarningCode_value},
	}

	for _, pair := range pairs {
		for number, name := range pair.flow {
			if got, ok := pair.observation["FLOW_"+name]; !ok || got != number {
				t.Errorf("FLOW_%s = %d (defined %v), want %d", name, got, ok, number)
			}
		}
	}
}

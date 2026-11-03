package connect

import (
	"fmt"
	"strconv"

	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
)

func newProtoFlowDiagnostics(
	raw []byte,
) ([]*observationv1.FlowElementState, []*observationv1.FlowWarning, error) {
	if raw == nil {
		return nil, nil, nil
	}

	diagnostics := new(flowv1.Diagnostics)
	if err := storedProtoJSON.Unmarshal(raw, diagnostics); err != nil {
		return nil, nil, fmt.Errorf("decode flow diagnostics: %w", err)
	}

	states := make([]*observationv1.FlowElementState, 0, len(diagnostics.GetElementStates()))
	for _, record := range diagnostics.GetElementStates() {
		skipped := make([]observationv1.FlowJudgement, 0, len(record.GetSkipped()))
		for _, judgement := range record.GetSkipped() {
			skipped = append(skipped, flowEnum[observationv1.FlowJudgement](judgement.String(), observationv1.FlowJudgement_value))
		}

		states = append(states, &observationv1.FlowElementState{
			TargetKey: record.GetTargetKey(),
			Observation: flowEnum[observationv1.FlowObservationState](
				record.GetObservation().String(), observationv1.FlowObservationState_value),
			History:    flowEnum[observationv1.FlowHistoryTier](record.GetHistory().String(), observationv1.FlowHistoryTier_value),
			LinePoints: record.GetLinePoints(),
			Skipped:    skipped,
		})
	}

	warnings := make([]*observationv1.FlowWarning, 0, len(diagnostics.GetWarnings()))
	for _, warning := range diagnostics.GetWarnings() {
		context := make(map[string]string, len(warning.GetContext()))
		for _, entry := range warning.GetContext() {
			context[entry.GetKey()] = scalarText(entry.GetValue())
		}

		warnings = append(warnings, &observationv1.FlowWarning{
			Code:    flowEnum[observationv1.FlowWarningCode](warning.GetCode().String(), observationv1.FlowWarningCode_value),
			Context: context,
		})
	}

	return states, warnings, nil
}

func flowEnum[T ~int32](name string, values map[string]int32) T {
	return T(values["FLOW_"+name])
}

func scalarText(value *flowv1.ScalarValue) string {
	switch kind := value.GetKind().(type) {
	case *flowv1.ScalarValue_StringValue:
		return kind.StringValue
	case *flowv1.ScalarValue_DoubleValue:
		return strconv.FormatFloat(kind.DoubleValue, 'g', -1, 64)
	case *flowv1.ScalarValue_IntValue:
		return strconv.FormatInt(kind.IntValue, 10)
	case *flowv1.ScalarValue_BoolValue:
		return strconv.FormatBool(kind.BoolValue)
	default:
		return ""
	}
}

package connect

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	operationv1 "github.com/pj-hoakari/tolo-operation/gen/tolo/operation/v1"
)

func TestOperationFlowProposals(t *testing.T) {
	solvedAt := timestamppb.New(time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC))

	tests := []struct {
		name   string
		result *flowv1.OptimizationResult
		want   *operationv1.FlowProposals
	}{
		{
			name: "route importances map edge directions onto the route's from-to direction",
			result: &flowv1.OptimizationResult{
				RouteImportance: []*flowv1.RouteImportance{
					{EdgeId: "r1", Direction: flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_A_TO_B, Importance: 0.9, ValidUntilNextTrigger: true},
					{EdgeId: "r2", Direction: flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_B_TO_A, Importance: 0.5},
					{EdgeId: "r3", Direction: flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_NONE, Importance: 0.2},
					{EdgeId: "r4", Direction: flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_UNSPECIFIED, Importance: 0.1},
					{EdgeId: "r5", Direction: flowv1.ImportanceDirection(99), Importance: 0.3},
				},
				SolvedAt: solvedAt,
			},
			want: &operationv1.FlowProposals{
				RouteImportances: []*operationv1.RouteImportance{
					{RouteId: "r1", Direction: operationv1.RouteDirection_ROUTE_DIRECTION_FORWARD.Enum(), Importance: 0.9},
					{RouteId: "r2", Direction: operationv1.RouteDirection_ROUTE_DIRECTION_BACKWARD.Enum(), Importance: 0.5},
					{RouteId: "r3", Direction: nil, Importance: 0.2},
					{RouteId: "r4", Direction: operationv1.RouteDirection_ROUTE_DIRECTION_UNSPECIFIED.Enum(), Importance: 0.1},
					{RouteId: "r5", Direction: operationv1.RouteDirection_ROUTE_DIRECTION_UNSPECIFIED.Enum(), Importance: 0.3},
				},
				SolvedAt: solvedAt,
			},
		},
		{
			name: "direction proposals",
			result: &flowv1.OptimizationResult{
				DirectionProposal: []*flowv1.DirectionProposal{
					{
						EdgeId: "r1", ProposedDirection: flowv1.ProposedDirection_PROPOSED_DIRECTION_A_TO_B,
						Confidence: 0.8, ChangeType: flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_CONVERT_ONEWAY,
					},
					{
						EdgeId: "r2", ProposedDirection: flowv1.ProposedDirection_PROPOSED_DIRECTION_B_TO_A,
						Confidence: 0.7, ChangeType: flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_FLIP_ONEWAY,
					},
					{
						EdgeId: "r3", ProposedDirection: flowv1.ProposedDirection_PROPOSED_DIRECTION_BIDIRECTIONAL,
						Confidence: 0.6, ChangeType: flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_RELEASE_ONEWAY,
					},
					{
						EdgeId: "r4", ProposedDirection: flowv1.ProposedDirection(99),
						Confidence: 0.5, ChangeType: flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_KEEP,
					},
					{
						EdgeId: "r5", ProposedDirection: flowv1.ProposedDirection_PROPOSED_DIRECTION_A_TO_B,
						Confidence: 0.4, ChangeType: flowv1.DirectionChangeType(99),
					},
				},
			},
			want: &operationv1.FlowProposals{
				DirectionProposals: []*operationv1.DirectionProposal{
					{
						RouteId: "r1", ProposedDirection: operationv1.RouteDirection_ROUTE_DIRECTION_FORWARD,
						Change: operationv1.DirectionChange_DIRECTION_CHANGE_CONVERT_ONE_WAY, Confidence: 0.8,
					},
					{
						RouteId: "r2", ProposedDirection: operationv1.RouteDirection_ROUTE_DIRECTION_BACKWARD,
						Change: operationv1.DirectionChange_DIRECTION_CHANGE_FLIP_ONE_WAY, Confidence: 0.7,
					},
					{
						RouteId: "r3", ProposedDirection: operationv1.RouteDirection_ROUTE_DIRECTION_BOTH_WAYS,
						Change: operationv1.DirectionChange_DIRECTION_CHANGE_RELEASE_ONE_WAY, Confidence: 0.6,
					},
					{
						RouteId: "r4", ProposedDirection: operationv1.RouteDirection_ROUTE_DIRECTION_UNSPECIFIED,
						Change: operationv1.DirectionChange_DIRECTION_CHANGE_KEEP, Confidence: 0.5,
					},
					{
						RouteId: "r5", ProposedDirection: operationv1.RouteDirection_ROUTE_DIRECTION_FORWARD,
						Change: operationv1.DirectionChange_DIRECTION_CHANGE_UNSPECIFIED, Confidence: 0.4,
					},
				},
			},
		},
		{
			name: "restriction proposals",
			result: &flowv1.OptimizationResult{
				RestrictionProposal: []*flowv1.RestrictionProposal{
					{
						EdgeId: "r1", Action: flowv1.RestrictionAction_RESTRICTION_ACTION_LIMIT, LimitValue: proto.Float64(40),
						Reason: flowv1.RestrictionReason_RESTRICTION_REASON_RESIDUAL_TAU, Confidence: 0.9, ScopeNote: "dropped",
					},
					{
						EdgeId: "r2", Action: flowv1.RestrictionAction_RESTRICTION_ACTION_CLOSE,
						Reason: flowv1.RestrictionReason_RESTRICTION_REASON_NODE_DANGER_UPSTREAM, Confidence: 0.8,
					},
					{
						EdgeId: "r3", Action: flowv1.RestrictionAction_RESTRICTION_ACTION_RESUME,
						Reason: flowv1.RestrictionReason_RESTRICTION_REASON_UNDRAINABLE_STAGNATION, Confidence: 0.7,
					},
					{
						EdgeId: "r4", Action: flowv1.RestrictionAction(99),
						Reason: flowv1.RestrictionReason_RESTRICTION_REASON_PUNCTURE, Confidence: 0.6,
					},
					{
						EdgeId: "r5", Action: flowv1.RestrictionAction_RESTRICTION_ACTION_CLOSE,
						Reason: flowv1.RestrictionReason_RESTRICTION_REASON_OVERLOAD, Confidence: 0.5,
					},
					{
						EdgeId: "r6", Action: flowv1.RestrictionAction_RESTRICTION_ACTION_CLOSE,
						Reason: flowv1.RestrictionReason(99), Confidence: 0.4,
					},
				},
			},
			want: &operationv1.FlowProposals{
				RestrictionProposals: []*operationv1.RestrictionProposal{
					{
						RouteId: "r1", Action: operationv1.RestrictionAction_RESTRICTION_ACTION_LIMIT, LimitValue: proto.Float64(40),
						Reason: operationv1.RestrictionReason_RESTRICTION_REASON_RESIDUAL_STAGNATION, Confidence: 0.9,
					},
					{
						RouteId: "r2", Action: operationv1.RestrictionAction_RESTRICTION_ACTION_CLOSE,
						Reason: operationv1.RestrictionReason_RESTRICTION_REASON_DANGER_UPSTREAM, Confidence: 0.8,
					},
					{
						RouteId: "r3", Action: operationv1.RestrictionAction_RESTRICTION_ACTION_RESUME,
						Reason: operationv1.RestrictionReason_RESTRICTION_REASON_UNDRAINABLE_STAGNATION, Confidence: 0.7,
					},
					{
						RouteId: "r4", Action: operationv1.RestrictionAction_RESTRICTION_ACTION_UNSPECIFIED,
						Reason: operationv1.RestrictionReason_RESTRICTION_REASON_PUNCTURE, Confidence: 0.6,
					},
					{
						RouteId: "r5", Action: operationv1.RestrictionAction_RESTRICTION_ACTION_CLOSE,
						Reason: operationv1.RestrictionReason_RESTRICTION_REASON_OVERLOAD, Confidence: 0.5,
					},
					{
						RouteId: "r6", Action: operationv1.RestrictionAction_RESTRICTION_ACTION_CLOSE,
						Reason: operationv1.RestrictionReason_RESTRICTION_REASON_UNSPECIFIED, Confidence: 0.4,
					},
				},
			},
		},
		{
			name: "detour proposals keep the route order",
			result: &flowv1.OptimizationResult{
				DetourPaths: []*flowv1.DetourPathProposal{{OriginEdgeId: "r1", EdgeIds: []string{"r7", "r8", "r9"}, Confidence: 0.6}},
			},
			want: &operationv1.FlowProposals{
				DetourProposals: []*operationv1.DetourProposal{{OriginRouteId: "r1", RouteIds: []string{"r7", "r8", "r9"}, Confidence: 0.6}},
			},
		},
		{
			name: "boundary controls",
			result: &flowv1.OptimizationResult{
				BoundaryControl: []*flowv1.BoundaryControl{
					{NodeId: "p1", Action: flowv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_INGRESS, Reason: "dropped"},
					{NodeId: "p2", Action: flowv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_EGRESS},
					{NodeId: "p3", Action: flowv1.BoundaryAction_BOUNDARY_ACTION_RESUME},
					{NodeId: "p4", Action: flowv1.BoundaryAction(99)},
				},
			},
			want: &operationv1.FlowProposals{
				BoundaryControls: []*operationv1.BoundaryControlProposal{
					{PointId: "p1", Action: operationv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_INGRESS},
					{PointId: "p2", Action: operationv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_EGRESS},
					{PointId: "p3", Action: operationv1.BoundaryAction_BOUNDARY_ACTION_RESUME},
					{PointId: "p4", Action: operationv1.BoundaryAction_BOUNDARY_ACTION_UNSPECIFIED},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := operationFlowProposals(tt.result)
			if !ok {
				t.Fatal("operationFlowProposals() reported no proposals")
			}

			if !proto.Equal(got, tt.want) {
				t.Errorf("operationFlowProposals() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOperationFlowProposalsReportsAResultWithoutProposals(t *testing.T) {
	result := &flowv1.OptimizationResult{
		ObjectiveValues: new(flowv1.OptimizationObjectiveValues),
		SolvedAt:        timestamppb.Now(),
	}

	if _, ok := operationFlowProposals(result); ok {
		t.Error("operationFlowProposals() reported proposals for a result that has only solved_at and objective values")
	}
}

func TestOperationEnumMapsCoverEveryFlowValue(t *testing.T) {
	importanceDirections := map[protoreflect.EnumNumber]bool{
		protoreflect.EnumNumber(flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_NONE): true,
	}
	for direction := range operationImportanceDirections {
		importanceDirections[protoreflect.EnumNumber(direction)] = true
	}

	tests := []struct {
		name   string
		values protoreflect.EnumValueDescriptors
		mapped map[protoreflect.EnumNumber]bool
	}{
		{"ImportanceDirection", flowv1.ImportanceDirection(0).Descriptor().Values(), importanceDirections},
		{"ProposedDirection", flowv1.ProposedDirection(0).Descriptor().Values(), enumKeys(operationProposedDirections)},
		{"DirectionChangeType", flowv1.DirectionChangeType(0).Descriptor().Values(), enumKeys(operationDirectionChanges)},
		{"RestrictionAction", flowv1.RestrictionAction(0).Descriptor().Values(), enumKeys(operationRestrictionActions)},
		{"RestrictionReason", flowv1.RestrictionReason(0).Descriptor().Values(), enumKeys(operationRestrictionReasons)},
		{"BoundaryAction", flowv1.BoundaryAction(0).Descriptor().Values(), enumKeys(operationBoundaryActions)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := range tt.values.Len() {
				if value := tt.values.Get(i); !tt.mapped[value.Number()] {
					t.Errorf("%s has no Operation counterpart", value.Name())
				}
			}
		})
	}
}

func enumKeys[K ~int32, V any](m map[K]V) map[protoreflect.EnumNumber]bool {
	keys := make(map[protoreflect.EnumNumber]bool, len(m))
	for key := range m {
		keys[protoreflect.EnumNumber(key)] = true
	}

	return keys
}

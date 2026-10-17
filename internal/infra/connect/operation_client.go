package connect

import (
	"context"
	"fmt"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/encoding/protojson"

	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	operationv1 "github.com/pj-hoakari/tolo-operation/gen/tolo/operation/v1"
	"github.com/pj-hoakari/tolo-operation/gen/tolo/operation/v1/operationv1connect"
)

type OperationClient struct {
	client operationv1connect.DeliveryCoordinationServiceClient
}

func NewOperationClient(httpClient connecthttp.HTTPClient, baseURL string) *OperationClient {
	return &OperationClient{
		client: operationv1connect.NewDeliveryCoordinationServiceClient(
			connectrpc.NewClient(connecthttp.NewTransport(httpClient, baseURL), forwardAuthorization()),
		),
	}
}

func (c *OperationClient) RequestProposalDelivery(
	ctx context.Context,
	tenantPublicID, eventID string,
	outcome domain.OptimizationOutcome,
) error {
	if outcome.OptimizationResult == nil {
		return nil
	}

	result := new(flowv1.OptimizationResult)
	if err := protojson.Unmarshal(outcome.OptimizationResult, result); err != nil {
		return fmt.Errorf("decode optimization result: %w", err)
	}

	proposals, ok := operationFlowProposals(result)
	if !ok {
		return nil
	}

	if _, err := c.client.RequestProposalDelivery(ctx, &operationv1.RequestProposalDeliveryRequest{
		EventId:  eventID,
		Payload:  &operationv1.RequestProposalDeliveryRequest_FlowProposals{FlowProposals: proposals},
		TenantId: tenantPublicID,
	}); err != nil {
		return fmt.Errorf("call RequestProposalDelivery: %w", err)
	}

	return nil
}

func operationFlowProposals(result *flowv1.OptimizationResult) (*operationv1.FlowProposals, bool) {
	proposals := &operationv1.FlowProposals{
		RouteImportances:     make([]*operationv1.RouteImportance, 0, len(result.GetRouteImportance())),
		DirectionProposals:   make([]*operationv1.DirectionProposal, 0, len(result.GetDirectionProposal())),
		RestrictionProposals: make([]*operationv1.RestrictionProposal, 0, len(result.GetRestrictionProposal())),
		DetourProposals:      make([]*operationv1.DetourProposal, 0, len(result.GetDetourPaths())),
		BoundaryControls:     make([]*operationv1.BoundaryControlProposal, 0, len(result.GetBoundaryControl())),
		SolvedAt:             result.GetSolvedAt(),
	}

	for _, importance := range result.GetRouteImportance() {
		proposals.RouteImportances = append(proposals.RouteImportances, &operationv1.RouteImportance{
			RouteId:    importance.GetEdgeId(),
			Direction:  operationImportanceDirection(importance.GetDirection()),
			Importance: importance.GetImportance(),
		})
	}

	for _, proposal := range result.GetDirectionProposal() {
		proposals.DirectionProposals = append(proposals.DirectionProposals, &operationv1.DirectionProposal{
			RouteId:           proposal.GetEdgeId(),
			ProposedDirection: operationProposedDirections[proposal.GetProposedDirection()],
			Change:            operationDirectionChanges[proposal.GetChangeType()],
			Confidence:        proposal.GetConfidence(),
		})
	}

	for _, proposal := range result.GetRestrictionProposal() {
		proposals.RestrictionProposals = append(proposals.RestrictionProposals, &operationv1.RestrictionProposal{
			RouteId:    proposal.GetEdgeId(),
			Action:     operationRestrictionActions[proposal.GetAction()],
			LimitValue: proposal.LimitValue,
			Reason:     operationRestrictionReasons[proposal.GetReason()],
			Confidence: proposal.GetConfidence(),
		})
	}

	for _, detour := range result.GetDetourPaths() {
		proposals.DetourProposals = append(proposals.DetourProposals, &operationv1.DetourProposal{
			OriginRouteId: detour.GetOriginEdgeId(),
			RouteIds:      detour.GetEdgeIds(),
			Confidence:    detour.GetConfidence(),
		})
	}

	for _, control := range result.GetBoundaryControl() {
		proposals.BoundaryControls = append(proposals.BoundaryControls, &operationv1.BoundaryControlProposal{
			PointId: control.GetNodeId(),
			Action:  operationBoundaryActions[control.GetAction()],
		})
	}

	empty := len(proposals.RouteImportances) == 0 && len(proposals.DirectionProposals) == 0 &&
		len(proposals.RestrictionProposals) == 0 && len(proposals.DetourProposals) == 0 &&
		len(proposals.BoundaryControls) == 0

	return proposals, !empty
}

func operationImportanceDirection(direction flowv1.ImportanceDirection) *operationv1.RouteDirection {
	if direction == flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_NONE {
		return nil
	}

	return operationImportanceDirections[direction].Enum()
}

var operationImportanceDirections = map[flowv1.ImportanceDirection]operationv1.RouteDirection{
	flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_UNSPECIFIED: operationv1.RouteDirection_ROUTE_DIRECTION_UNSPECIFIED,
	flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_A_TO_B:      operationv1.RouteDirection_ROUTE_DIRECTION_FORWARD,
	flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_B_TO_A:      operationv1.RouteDirection_ROUTE_DIRECTION_BACKWARD,
}

var operationProposedDirections = map[flowv1.ProposedDirection]operationv1.RouteDirection{
	flowv1.ProposedDirection_PROPOSED_DIRECTION_UNSPECIFIED:   operationv1.RouteDirection_ROUTE_DIRECTION_UNSPECIFIED,
	flowv1.ProposedDirection_PROPOSED_DIRECTION_A_TO_B:        operationv1.RouteDirection_ROUTE_DIRECTION_FORWARD,
	flowv1.ProposedDirection_PROPOSED_DIRECTION_B_TO_A:        operationv1.RouteDirection_ROUTE_DIRECTION_BACKWARD,
	flowv1.ProposedDirection_PROPOSED_DIRECTION_BIDIRECTIONAL: operationv1.RouteDirection_ROUTE_DIRECTION_BOTH_WAYS,
}

var operationDirectionChanges = map[flowv1.DirectionChangeType]operationv1.DirectionChange{
	flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_UNSPECIFIED:    operationv1.DirectionChange_DIRECTION_CHANGE_UNSPECIFIED,
	flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_CONVERT_ONEWAY: operationv1.DirectionChange_DIRECTION_CHANGE_CONVERT_ONE_WAY,
	flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_RELEASE_ONEWAY: operationv1.DirectionChange_DIRECTION_CHANGE_RELEASE_ONE_WAY,
	flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_FLIP_ONEWAY:    operationv1.DirectionChange_DIRECTION_CHANGE_FLIP_ONE_WAY,
	flowv1.DirectionChangeType_DIRECTION_CHANGE_TYPE_KEEP:           operationv1.DirectionChange_DIRECTION_CHANGE_KEEP,
}

var operationRestrictionActions = map[flowv1.RestrictionAction]operationv1.RestrictionAction{
	flowv1.RestrictionAction_RESTRICTION_ACTION_UNSPECIFIED: operationv1.RestrictionAction_RESTRICTION_ACTION_UNSPECIFIED,
	flowv1.RestrictionAction_RESTRICTION_ACTION_CLOSE:       operationv1.RestrictionAction_RESTRICTION_ACTION_CLOSE,
	flowv1.RestrictionAction_RESTRICTION_ACTION_LIMIT:       operationv1.RestrictionAction_RESTRICTION_ACTION_LIMIT,
	flowv1.RestrictionAction_RESTRICTION_ACTION_RESUME:      operationv1.RestrictionAction_RESTRICTION_ACTION_RESUME,
}

var operationRestrictionReasons = map[flowv1.RestrictionReason]operationv1.RestrictionReason{
	flowv1.RestrictionReason_RESTRICTION_REASON_UNSPECIFIED:            operationv1.RestrictionReason_RESTRICTION_REASON_UNSPECIFIED,
	flowv1.RestrictionReason_RESTRICTION_REASON_UNDRAINABLE_STAGNATION: operationv1.RestrictionReason_RESTRICTION_REASON_UNDRAINABLE_STAGNATION,
	flowv1.RestrictionReason_RESTRICTION_REASON_RESIDUAL_TAU:           operationv1.RestrictionReason_RESTRICTION_REASON_RESIDUAL_STAGNATION,
	flowv1.RestrictionReason_RESTRICTION_REASON_PUNCTURE:               operationv1.RestrictionReason_RESTRICTION_REASON_PUNCTURE,
	flowv1.RestrictionReason_RESTRICTION_REASON_NODE_DANGER_UPSTREAM:   operationv1.RestrictionReason_RESTRICTION_REASON_DANGER_UPSTREAM,
	flowv1.RestrictionReason_RESTRICTION_REASON_OVERLOAD:               operationv1.RestrictionReason_RESTRICTION_REASON_OVERLOAD,
}

var operationBoundaryActions = map[flowv1.BoundaryAction]operationv1.BoundaryAction{
	flowv1.BoundaryAction_BOUNDARY_ACTION_UNSPECIFIED:   operationv1.BoundaryAction_BOUNDARY_ACTION_UNSPECIFIED,
	flowv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_INGRESS: operationv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_INGRESS,
	flowv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_EGRESS:  operationv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_EGRESS,
	flowv1.BoundaryAction_BOUNDARY_ACTION_RESUME:        operationv1.BoundaryAction_BOUNDARY_ACTION_RESUME,
}

package connect

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"

	connectrpc "connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1/flowv1connect"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

const flowRequestSchemaVersion = "request/1"

type FlowControlClient struct {
	client flowv1connect.FlowControlServiceClient
}

func NewFlowControlClient(baseURL string) *FlowControlClient {
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)

	httpClient := &http.Client{Transport: &http.Transport{Protocols: protocols}}

	return &FlowControlClient{
		client: flowv1connect.NewFlowControlServiceClient(httpClient, baseURL, connectrpc.WithGRPC()),
	}
}

func (c *FlowControlClient) Optimize(
	ctx context.Context,
	input application.OptimizeInput,
) (domain.OptimizationOutcome, error) {
	req, err := newOptimizeRequest(input)
	if err != nil {
		return domain.OptimizationOutcome{}, err
	}

	res, err := c.client.Optimize(ctx, connectrpc.NewRequest(req))
	if err != nil {
		return domain.OptimizationOutcome{}, fmt.Errorf("call Optimize: %w", err)
	}

	detectionState, err := protojson.Marshal(res.Msg.GetUpdatedDetectionState())
	if err != nil {
		return domain.OptimizationOutcome{}, fmt.Errorf("encode detection state: %w", err)
	}

	outcome := domain.OptimizationOutcome{
		SnapshotID:         input.Snapshot.ID,
		Verdict:            res.Msg.GetVerdict().String(),
		DetectionState:     detectionState,
		OptimizationResult: nil,
	}

	if res.Msg.OptimizationResult != nil {
		outcome.OptimizationResult, err = protojson.Marshal(res.Msg.GetOptimizationResult())
		if err != nil {
			return domain.OptimizationOutcome{}, fmt.Errorf("encode optimization result: %w", err)
		}
	}

	return outcome, nil
}

func newOptimizeRequest(input application.OptimizeInput) (*flowv1.OptimizeRequest, error) {
	snapshot := input.Snapshot
	resolution := int32(snapshot.WindowEnd.Sub(snapshot.WindowStart).Seconds())

	detectionState := new(flowv1.DetectionState)

	var previousResult *flowv1.OptimizationResult

	if input.Previous != nil {
		if err := protojson.Unmarshal(input.Previous.DetectionState, detectionState); err != nil {
			return nil, fmt.Errorf("decode previous detection state: %w", err)
		}

		if input.Previous.OptimizationResult != nil {
			previousResult = new(flowv1.OptimizationResult)
			if err := protojson.Unmarshal(input.Previous.OptimizationResult, previousResult); err != nil {
				return nil, fmt.Errorf("decode previous optimization result: %w", err)
			}
		}
	}

	graph, nodes, edges := flowGraph(input.Graph, resolution)

	return &flowv1.OptimizeRequest{
		RequestId:     proto.String(rand.Text()),
		SchemaVersion: proto.String(flowRequestSchemaVersion),
		EventId:       proto.String(snapshot.EventID),
		TenantContext: &flowv1.TenantContext{
			TenantId:              proto.String(input.TenantPublicID),
			TenantCategory:        flowv1.TenantCategory_TENANT_CATEGORY_SHORT_TERM.Enum(),
			AvailableHistoryHours: proto.Float64(0),
		},
		Graph:          graph,
		Observations:   flowObservations(snapshot, nodes, edges),
		HistoryDigest:  new(flowv1.HistoryDigest),
		DetectionState: detectionState,
		References:     new(flowv1.Reference),
		PreviousResult: previousResult,
		Events:         nil,
		Config:         defaultFlowConfig(),
		ServerTime:     timestamppb.New(input.ServerTime),
	}, nil
}

func flowGraph(graph domain.Graph, resolution int32) (*flowv1.Graph, map[string]bool, map[string]bool) {
	nodes := make([]*flowv1.Node, 0, len(graph.Points))
	nodeIDs := make(map[string]bool, len(graph.Points))

	for _, point := range graph.Points {
		nodeIDs[point.ID] = true
		nodes = append(nodes, &flowv1.Node{
			NodeId:          proto.String(point.ID),
			Kind:            flowNodeKind(point.Type).Enum(),
			IsBoundary:      proto.Bool(point.IsBoundary),
			Enabled:         proto.Bool(!point.IsBoundary || point.BoundaryActive),
			AttributeTags:   nil,
			TimeResolutionS: proto.Int32(resolution),
			DangerFlag:      proto.Bool(false),
			DangerCapacity:  nil,
		})
	}

	edges := make([]*flowv1.Edge, 0, len(graph.Routes))
	edgeIDs := make(map[string]bool, len(graph.Routes))

	for _, route := range graph.Routes {
		constraint := flowv1.DirectionConstraint_DIRECTION_CONSTRAINT_BIDIRECTIONAL_PRIOR
		current := flowv1.CurrentDirection_CURRENT_DIRECTION_BIDIRECTIONAL

		if route.OneWay {
			constraint = flowv1.DirectionConstraint_DIRECTION_CONSTRAINT_ONEWAY_A_TO_B_PRIOR
			current = flowv1.CurrentDirection_CURRENT_DIRECTION_A_TO_B
		}

		edgeIDs[route.ID] = true
		edges = append(edges, &flowv1.Edge{
			EdgeId:              proto.String(route.ID),
			EndpointA:           proto.String(route.FromPointID),
			EndpointB:           proto.String(route.ToPointID),
			DirectionConstraint: constraint.Enum(),
			CurrentDirection:    current.Enum(),
			Enabled:             proto.Bool(true),
			ObservationType:     flowv1.ObservationType_OBSERVATION_TYPE_VECTOR.Enum(),
			AttributeTags:       nil,
			TimeResolutionS:     proto.Int32(resolution),
			DangerFlag:          proto.Bool(false),
			DangerCapacity:      nil,
			CapacityHint:        route.CapacityHint,
		})
	}

	return &flowv1.Graph{Nodes: nodes, Edges: edges}, nodeIDs, edgeIDs
}

func flowNodeKind(pointType domain.PointType) flowv1.NodeKind {
	switch pointType {
	case domain.PointTypeGoal:
		return flowv1.NodeKind_NODE_KIND_GOAL
	case domain.PointTypeGoalTransitMixed:
		return flowv1.NodeKind_NODE_KIND_GOAL_TRANSIT_MIXED
	case domain.PointTypeTransitOnly:
		return flowv1.NodeKind_NODE_KIND_TRANSIT_ONLY
	case domain.PointTypeUnspecified:
		return flowv1.NodeKind_NODE_KIND_UNSPECIFIED
	default:
		return flowv1.NodeKind_NODE_KIND_UNSPECIFIED
	}
}

func flowObservations(snapshot domain.Snapshot, nodeIDs, edgeIDs map[string]bool) *flowv1.Observations {
	ok := flowv1.ConfidenceFlag_CONFIDENCE_FLAG_OK

	observations := &flowv1.Observations{
		ObservedAt:      timestamppb.New(snapshot.WindowEnd),
		SnapshotRef:     proto.String(snapshot.ID),
		ArcFlows:        nil,
		ArcStagnations:  nil,
		ArcScalarFlows:  nil,
		NodeOccupancies: nil,
		NodeTurning:     nil,
	}

	for _, score := range snapshot.RouteScores {
		if !edgeIDs[score.RouteID] {
			continue
		}

		observations.ArcFlows = append(observations.ArcFlows,
			&flowv1.ArcFlow{
				EdgeId:         proto.String(score.RouteID),
				Direction:      flowv1.FlowDirection_FLOW_DIRECTION_A_TO_B.Enum(),
				FlowRate:       proto.Float64(score.Forward),
				ConfidenceFlag: ok.Enum(),
			},
			&flowv1.ArcFlow{
				EdgeId:         proto.String(score.RouteID),
				Direction:      flowv1.FlowDirection_FLOW_DIRECTION_B_TO_A.Enum(),
				FlowRate:       proto.Float64(score.Backward),
				ConfidenceFlag: ok.Enum(),
			},
		)
	}

	for _, score := range snapshot.PointScores {
		if !nodeIDs[score.PointID] {
			continue
		}

		observations.NodeOccupancies = append(observations.NodeOccupancies, &flowv1.NodeOccupancy{
			NodeId:            proto.String(score.PointID),
			Occupancy:         proto.Float64(score.PeopleScore),
			OccupancyDelta:    proto.Float64(score.OccupancyDelta),
			SameSensorArrival: proto.Bool(false),
			ConfidenceFlag:    ok.Enum(),
		})
	}

	return observations
}

func defaultFlowConfig() *flowv1.ResolvedConfig {
	return &flowv1.ResolvedConfig{
		SurgeRateThresholdPercentPerMin: proto.Float64(10),
		HighStagnationDurationMin:       proto.Float64(5),
		Beta:                            proto.Float64(1),
		ThetaDemand:                     nil,
		MinWindowSamples:                proto.Int32(5),
		CooldownDurationMin:             proto.Float64(60),
		WarmupDurationMin:               proto.Float64(60),
		RetriggerWarningThreshold:       proto.Int32(3),
		RetriggerResetQuietCycles:       proto.Int32(3),
		QueueScoreThreshold:             proto.Float64(5),
		QueueDiversityThreshold:         proto.Int32(3),
		QueueFreshnessMin:               nil,
		ThroughputTargetEdges:           nil,
		OptimizationMode:                flowv1.OptimizationMode_OPTIMIZATION_MODE_LIGHTWEIGHT.Enum(),
		LocalRadiusHops:                 proto.Int32(2),
		MaxTriggerZones:                 proto.Int32(4),
		GreedyImproveMargin:             proto.Float64(0.05),
		RestrictionProposalEnabled:      proto.Bool(false),
		TauDangerThreshold:              nil,
		PunctureTriggerEnabled:          proto.Bool(false),
		PunctureRatioThreshold:          proto.Float64(1),
		ForecastingBudgetSec:            proto.Float64(30),
		DetourBudgetSec:                 proto.Float64(30),
		LightweightOptBudgetSec:         proto.Float64(120),
		MilpTimeLimitSec:                proto.Float64(600),
		MaxConsecutiveSkips:             proto.Int32(3),
		SolverSeed:                      proto.Int32(0),
		Epsilon:                         proto.Float64(1e-3),
		Epsilon_0:                       proto.Float64(1e-6),
		BigMFactor:                      proto.Float64(1),
		DeltaMin:                        proto.Float64(0.5),
		ConfidenceWeightFloor:           proto.Float64(0.5),
		MipRelGap:                       proto.Float64(0),
		GravityAlpha:                    proto.Float64(1),
		IpfMaxIter:                      proto.Int32(50),
		IpfTolerance:                    proto.Float64(1e-6),
		TransitTimePriorSec:             nil,
		DwellTimePriorSec:               nil,
		MinReferenceSampleCount:         proto.Int32(5),
		KShortestPaths:                  proto.Int32(3),
		FallbackEta:                     proto.Float64(1),
		FallbackBaselineStagnation:      proto.Float64(1),
		ScalarDirectionEnabled:          proto.Bool(false),
		KShortestPathsAuto:              proto.Bool(false),
		KShortestPathsMax:               proto.Int32(3),
		PhaseFeedbackEnabled:            proto.Bool(false),
		PhaseFeedbackCandidates:         proto.Int32(0),
		AttributeTagPriority:            nil,
		FullWeightHours:                 proto.Float64(0),
		EtaMixingRatio:                  proto.Float64(0),
		ThroughputWeights:               &flowv1.ThroughputWeights{TriggerOrigin: 1, DetourPath: 1, OperatorSet: 1},
		ThroughputMaxPerEdge:            nil,
		TwoStageOptimization:            proto.Bool(false),
		BackoffEnabled:                  proto.Bool(false),
		ImprovementThreshold:            proto.Float64(0),
		BackoffMaxFactor:                proto.Int32(1),
		ShadowExtensions:                nil,
		DeltaObjectiveEnabled:           proto.Bool(false),
		NodeDetourEnabled:               proto.Bool(false),
		MilpBoundaryControlEnabled:      proto.Bool(false),
		ScheduledInflowPriorEnabled:     proto.Bool(false),
		LogTenantId:                     proto.Bool(false),
	}
}

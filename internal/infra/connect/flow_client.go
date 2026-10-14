package connect

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"slices"
	"time"

	connectrpc "connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1/flowv1connect"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

const (
	flowRequestSchemaVersion = "request/1"
	minArcHistorySpan        = 4 * time.Hour
)

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
		ServerTime:         input.ServerTime,
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
			TenantId: proto.String(input.TenantPublicID),
		},
		EventContext: &flowv1.EventContext{
			Term:                  flowEventTerm(input.EventType).Enum(),
			AvailableHistoryHours: proto.Float64(input.AvailableHistory.Hours()),
		},
		Graph:          graph,
		Observations:   flowObservations(snapshot, input.Gaps, nodes, edges),
		HistoryDigest:  flowHistoryDigest(input.History, input.ArcHistory, edges),
		DetectionState: detectionState,
		References:     new(flowv1.Reference),
		PreviousResult: previousResult,
		Events:         flowEvents(input.SensorSetChanges, nodes, edges),
		Config:         defaultFlowConfig(),
		ServerTime:     timestamppb.New(input.ServerTime),
	}, nil
}

func flowEventTerm(eventType domain.EventType) flowv1.EventTerm {
	if eventType == domain.EventTypeLongTerm {
		return flowv1.EventTerm_EVENT_TERM_LONG
	}

	return flowv1.EventTerm_EVENT_TERM_SHORT
}

func flowGraph(graph domain.Graph, resolution int32) (*flowv1.Graph, map[string]bool, map[string]bool) {
	nodes := make([]*flowv1.Node, 0, len(graph.Points))
	nodeIDs := make(map[string]bool, len(graph.Points))

	for _, point := range graph.Points {
		nodeIDs[point.ID] = true
		nodes = append(nodes, &flowv1.Node{
			NodeId:          proto.String(point.ID),
			Kind:            flowNodeKind(point.Type).Enum(),
			Enabled:         proto.Bool(true),
			AttributeTags:   nil,
			TimeResolutionS: proto.Int32(resolution),
			DangerFlag:      proto.Bool(false),
			DangerCapacity:  nil,
			Boundary:        flowBoundary(point.Boundary),
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

func flowBoundary(boundary *domain.Boundary) *flowv1.Boundary {
	if boundary == nil {
		return nil
	}

	return &flowv1.Boundary{
		Direction: flowBoundaryDirection(boundary.Direction).Enum(),
		Active:    proto.Bool(boundary.Active),
	}
}

func flowBoundaryDirection(direction domain.BoundaryDirection) flowv1.BoundaryDirection {
	switch direction {
	case domain.BoundaryDirectionEntry:
		return flowv1.BoundaryDirection_BOUNDARY_DIRECTION_ENTRY
	case domain.BoundaryDirectionExit:
		return flowv1.BoundaryDirection_BOUNDARY_DIRECTION_EXIT
	case domain.BoundaryDirectionEntryAndExit:
		return flowv1.BoundaryDirection_BOUNDARY_DIRECTION_ENTRY_AND_EXIT
	case domain.BoundaryDirectionUnspecified:
		return flowv1.BoundaryDirection_BOUNDARY_DIRECTION_UNSPECIFIED
	default:
		return flowv1.BoundaryDirection_BOUNDARY_DIRECTION_UNSPECIFIED
	}
}

func flowObservations(snapshot domain.Snapshot, gaps domain.Gaps, nodeIDs, edgeIDs map[string]bool) *flowv1.Observations {
	observations := &flowv1.Observations{
		ObservedAt:      timestamppb.New(snapshot.WindowEnd),
		SnapshotRef:     proto.String(snapshot.ID),
		ArcFlows:        nil,
		ArcStagnations:  nil,
		ArcScalarFlows:  nil,
		NodeOccupancies: nil,
		NodeTurning:     nil,
	}

	addRoute := func(score domain.RouteScore, flag flowv1.ConfidenceFlag) {
		if edgeIDs[score.RouteID] && score.Exhaustive {
			appendArcObservations(observations, score, flag)
		}
	}

	addPoint := func(score domain.PointScore, flag flowv1.ConfidenceFlag) {
		if nodeIDs[score.PointID] && score.Exhaustive {
			delta := proto.Float64(score.OccupancyDelta)
			if flag == flowv1.ConfidenceFlag_CONFIDENCE_FLAG_HOLD {
				delta = nil
			}

			observations.NodeOccupancies = append(observations.NodeOccupancies, &flowv1.NodeOccupancy{
				NodeId:            proto.String(score.PointID),
				Occupancy:         score.PeopleScore,
				OccupancyDelta:    delta,
				SameSensorArrival: proto.Bool(false),
				ConfidenceFlag:    flag.Enum(),
			})
		}
	}

	for _, score := range snapshot.RouteScores {
		addRoute(score, flowv1.ConfidenceFlag_CONFIDENCE_FLAG_OK)
	}

	for _, score := range gaps.HeldRouteScores {
		addRoute(score, flowv1.ConfidenceFlag_CONFIDENCE_FLAG_HOLD)
	}

	for _, routeID := range gaps.InvalidRouteIDs {
		addRoute(domain.RouteScore{RouteID: routeID, Forward: 0, Backward: 0, StagnationScore: nil, Exhaustive: true},
			flowv1.ConfidenceFlag_CONFIDENCE_FLAG_INVALID)
	}

	for _, score := range snapshot.PointScores {
		addPoint(score, flowv1.ConfidenceFlag_CONFIDENCE_FLAG_OK)
	}

	for _, score := range gaps.HeldPointScores {
		addPoint(score, flowv1.ConfidenceFlag_CONFIDENCE_FLAG_HOLD)
	}

	for _, pointID := range gaps.InvalidPointIDs {
		addPoint(domain.PointScore{PointID: pointID, PeopleScore: nil, OccupancyDelta: 0, Exhaustive: true},
			flowv1.ConfidenceFlag_CONFIDENCE_FLAG_INVALID)
	}

	return observations
}

func flowEvents(changes []domain.SensorSet, nodeIDs, edgeIDs map[string]bool) []*flowv1.Event {
	var events []*flowv1.Event

	for _, change := range slices.SortedStableFunc(slices.Values(changes), func(a, b domain.SensorSet) int {
		return a.ChangedAt.Compare(b.ChangedAt)
	}) {
		var target string

		switch {
		case edgeIDs[change.Anchor.RouteID]:
			target = "edge:" + change.Anchor.RouteID
		case nodeIDs[change.Anchor.PointID]:
			target = "node:" + change.Anchor.PointID
		default:
			continue
		}

		events = append(events, &flowv1.Event{
			Kind:       flowv1.EventKind_EVENT_KIND_SENSOR_SET_CHANGED,
			TargetId:   target,
			OccurredAt: timestamppb.New(change.ChangedAt),
			Params:     nil,
		})
	}

	return events
}

func appendArcObservations(observations *flowv1.Observations, score domain.RouteScore, flag flowv1.ConfidenceFlag) {
	observations.ArcFlows = append(observations.ArcFlows,
		&flowv1.ArcFlow{
			EdgeId:         proto.String(score.RouteID),
			Direction:      flowv1.FlowDirection_FLOW_DIRECTION_A_TO_B.Enum(),
			FlowRate:       proto.Float64(score.Forward),
			ConfidenceFlag: flag.Enum(),
		},
		&flowv1.ArcFlow{
			EdgeId:         proto.String(score.RouteID),
			Direction:      flowv1.FlowDirection_FLOW_DIRECTION_B_TO_A.Enum(),
			FlowRate:       proto.Float64(score.Backward),
			ConfidenceFlag: flag.Enum(),
		},
	)

	if score.StagnationScore == nil {
		return
	}

	observations.ArcStagnations = append(observations.ArcStagnations, &flowv1.ArcStagnation{
		EdgeId:         proto.String(score.RouteID),
		Stagnation:     score.StagnationScore,
		Derivation:     flowv1.StagnationDerivation_STAGNATION_DERIVATION_BASIC.Enum(),
		SameSensorFlow: proto.Bool(false),
		ConfidenceFlag: flag.Enum(),
	})
}

func flowHistoryDigest(
	history []domain.Snapshot,
	arcHistory []domain.ArcHistory,
	edgeIDs map[string]bool,
) *flowv1.HistoryDigest {
	series := map[string]*flowv1.ArcWindowSeries{}
	digest := new(flowv1.HistoryDigest)

	for _, arc := range arcHistory {
		if !edgeIDs[arc.RouteID] {
			continue
		}

		span := arc.LastEnd.Sub(arc.FirstStart)
		stat := &flowv1.ArcHistoryStat{
			EdgeId:             arc.RouteID,
			P90Stagnation:      nil,
			BaselineStagnation: nil,
			FlowSensitivityEta: nil,
			AvailableSpanHours: proto.Float64(span.Hours()),
		}

		if span >= minArcHistorySpan {
			stat.P90Stagnation = proto.Float64(arc.P90Stagnation)
			stat.BaselineStagnation = proto.Float64(arc.MeanStagnation)

			if eta, ok := arc.FlowSensitivityEta(); ok {
				stat.FlowSensitivityEta = proto.Float64(eta)
			}
		}

		digest.ArcStats = append(digest.ArcStats, stat)
	}

	for _, snapshot := range history {
		at := timestamppb.New(snapshot.WindowEnd)

		for _, score := range snapshot.RouteScores {
			if !edgeIDs[score.RouteID] || !score.Exhaustive {
				continue
			}

			arc, ok := series[score.RouteID]
			if !ok {
				arc = &flowv1.ArcWindowSeries{
					EdgeId:                 score.RouteID,
					FlowSamples:            nil,
					StagnationSamples:      nil,
					DirectionalFlowSamples: nil,
				}
				series[score.RouteID] = arc
				digest.WindowSeries = append(digest.WindowSeries, arc)
			}

			arc.FlowSamples = append(arc.FlowSamples, &flowv1.TimedValue{At: at, Value: score.Forward + score.Backward})

			if score.StagnationScore != nil {
				arc.StagnationSamples = append(arc.StagnationSamples, &flowv1.TimedValue{At: at, Value: *score.StagnationScore})
			}
		}
	}

	return digest
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
		FallbackEta:                     proto.Float64(0.25),
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

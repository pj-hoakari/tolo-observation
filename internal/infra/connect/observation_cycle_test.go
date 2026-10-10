package connect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	connectrpc "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1/flowv1connect"
	graphv1 "github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1"
	"github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1/graphv1connect"
	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
)

const cycleEventPublicID = "1234abcd5678ef90"

type graphStub struct {
	graphv1connect.UnimplementedGraphSupplyServiceHandler

	mu             sync.Mutex
	mappings       []*graphv1.ObservationPointMapping
	authorizations []string
}

func startGraphStub(t *testing.T) (*graphStub, string) {
	t.Helper()

	stub := &graphStub{}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewGraphSupplyServiceHandler(stub))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return stub, server.URL
}

func (s *graphStub) mapTo(observationPointID string, anchor *kernelv1.GraphAnchor) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mappings = append(s.mappings, &graphv1.ObservationPointMapping{ObservationPointId: observationPointID, Anchor: anchor})
}

func (s *graphStub) GetCurrentRevision(
	_ context.Context,
	req *connectrpc.Request[graphv1.GetCurrentRevisionRequest],
) (*connectrpc.Response[kernelv1.Graph], error) {
	s.mu.Lock()
	s.authorizations = append(s.authorizations, req.Header().Get("Authorization"))
	s.mu.Unlock()

	return connectrpc.NewResponse(&kernelv1.Graph{
		EventId:    req.Msg.GetEventId(),
		RevisionId: "rev-1",
		Points: []*kernelv1.Point{
			{PointId: "p1", Type: kernelv1.PointType_POINT_TYPE_GOAL},
			{PointId: "p2", Type: kernelv1.PointType_POINT_TYPE_TRANSIT_ONLY},
		},
		Routes: []*kernelv1.Route{
			{RouteId: "r1", FromPointId: "p1", ToPointId: "p2", Direction: kernelv1.DirectionAttribute_DIRECTION_ATTRIBUTE_BOTH_WAYS},
		},
	}), nil
}

func (s *graphStub) GetObservationPointMappings(
	_ context.Context,
	_ *connectrpc.Request[graphv1.GetMappingsRequest],
) (*connectrpc.Response[graphv1.GetMappingsResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return connectrpc.NewResponse(&graphv1.GetMappingsResponse{RevisionId: "rev-1", Mappings: s.mappings}), nil
}

type flowStub struct {
	flowv1connect.UnimplementedFlowControlServiceHandler

	mu             sync.Mutex
	requests       []*flowv1.OptimizeRequest
	authorizations []string
	unavailable    bool
}

func startFlowStub(t *testing.T) (*flowStub, string) {
	t.Helper()

	stub := &flowStub{}
	mux := http.NewServeMux()
	mux.Handle(flowv1connect.NewFlowControlServiceHandler(stub))

	server := httptest.NewUnstartedServer(mux)
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	t.Cleanup(server.Close)

	return stub, server.URL
}

func (s *flowStub) Optimize(
	_ context.Context,
	req *connectrpc.Request[flowv1.OptimizeRequest],
) (*connectrpc.Response[flowv1.OptimizeResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.unavailable {
		return nil, connectrpc.NewError(connectrpc.CodeUnavailable, errors.New("flow control is down"))
	}

	if req.Peer().Protocol != connectrpc.ProtocolGRPC {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, errors.New("flow control serves gRPC only"))
	}

	s.requests = append(s.requests, req.Msg)
	s.authorizations = append(s.authorizations, req.Header().Get("Authorization"))

	return connectrpc.NewResponse(&flowv1.OptimizeResponse{
		RequestId: proto.String(req.Msg.GetRequestId()),
		Verdict:   flowv1.Verdict_VERDICT_SKIPPED_NO_TRIGGER.Enum(),
		UpdatedDetectionState: &flowv1.DetectionState{
			ConsecutiveSkipCount: int32(len(s.requests)),
		},
	}), nil
}

func (s *flowStub) setUnavailable(unavailable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.unavailable = unavailable
}

func (s *flowStub) received() ([]*flowv1.OptimizeRequest, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]*flowv1.OptimizeRequest(nil), s.requests...), append([]string(nil), s.authorizations...)
}

func TestObservationCycle(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(ctx, authorized(
		flow.token(t, cycleEventPublicID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               cycleEventPublicID,
			Name:                  "gate-1",
			ObservationPointNames: []string{"corridor", "hall"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	deviceID := registered.Msg.GetDevice().GetEdgeDeviceId()
	corridor := registered.Msg.GetDevice().GetObservationPoints()[0].GetObservationPointId()
	hall := registered.Msg.GetDevice().GetObservationPoints()[1].GetObservationPointId()

	flow.graph.mapTo(corridor, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})
	flow.graph.mapTo(hall, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p2"}})

	reportToken := flow.token(t, cycleEventPublicID, "events.report")

	if _, err := flow.edgeDevices.Heartbeat(ctx, authorized(reportToken, &observationv1.HeartbeatRequest{
		EventId:                   cycleEventPublicID,
		EdgeDeviceId:              deviceID,
		ActiveObservationPointIds: []string{corridor, hall},
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	report := func(t *testing.T) {
		t.Helper()

		reported, err := flow.measurements.ReportMeasurements(ctx, authorized(reportToken, &observationv1.ReportMeasurementsRequest{
			EventId:      cycleEventPublicID,
			EdgeDeviceId: deviceID,
			Measurements: []*observationv1.Measurement{
				edgeMeasurement(corridor, flow.observedStart),
				edgeMeasurement(hall, flow.observedStart),
			},
		}))
		if err != nil {
			t.Fatalf("ReportMeasurements() error = %v", err)
		}

		if got, want := reported.Msg.GetAcceptedCount(), int32(2); got != want {
			t.Fatalf("accepted_count = %d, want %d", got, want)
		}
	}

	report(t)

	requests, flowAuthorizations := flow.flow.received()
	if len(requests) != 1 {
		t.Fatalf("Optimize received %d requests, want 1", len(requests))
	}

	optimize := requests[0]
	if optimize.GetEventId() != cycleEventPublicID || optimize.GetTenantContext().GetTenantId() != testTenantPublicID {
		t.Errorf("Optimize event/tenant = %q/%q", optimize.GetEventId(), optimize.GetTenantContext().GetTenantId())
	}

	if got := len(optimize.GetGraph().GetNodes()) + len(optimize.GetGraph().GetEdges()); got != 3 {
		t.Errorf("Optimize graph has %d elements, want 3", got)
	}

	flows := map[flowv1.FlowDirection]float64{}
	for _, arc := range optimize.GetObservations().GetArcFlows() {
		flows[arc.GetDirection()] = arc.GetFlowRate()
	}

	if flows[flowv1.FlowDirection_FLOW_DIRECTION_A_TO_B] != 7 || flows[flowv1.FlowDirection_FLOW_DIRECTION_B_TO_A] != 3 {
		t.Errorf("Optimize arc flows = %v, want 7 forward and 3 backward on r1", optimize.GetObservations().GetArcFlows())
	}

	occupancies := optimize.GetObservations().GetNodeOccupancies()
	if len(occupancies) != 1 || occupancies[0].GetNodeId() != "p2" ||
		occupancies[0].GetOccupancy() != 2.5 || occupancies[0].GetOccupancyDelta() != 4 {
		t.Errorf("Optimize node occupancies = %v, want 2.5 people and a delta of 4 on p2", occupancies)
	}

	stagnations := optimize.GetObservations().GetArcStagnations()
	if len(stagnations) != 1 || stagnations[0].GetEdgeId() != "r1" || stagnations[0].GetStagnation() != 2.5 ||
		stagnations[0].GetDerivation() != flowv1.StagnationDerivation_STAGNATION_DERIVATION_BASIC {
		t.Errorf("Optimize arc stagnations = %v, want 2.5 detected people on r1", stagnations)
	}

	if series := optimize.GetHistoryDigest().GetWindowSeries(); len(series) != 0 {
		t.Errorf("first Optimize window series = %v, want none before any past snapshot", series)
	}

	if flowAuthorizations[0] != "" {
		t.Errorf("Optimize carried Authorization %q, want none", flowAuthorizations[0])
	}

	flow.graph.mu.Lock()
	graphAuthorization := flow.graph.authorizations[len(flow.graph.authorizations)-1]
	flow.graph.mu.Unlock()

	if graphAuthorization != reportToken {
		t.Errorf("GetCurrentRevision carried Authorization %q, want the received token", graphAuthorization)
	}

	overview, err := flow.statusQueries.GetEventOverview(ctx, authorized(
		flow.token(t, cycleEventPublicID, "events.read"),
		&observationv1.GetEventOverviewRequest{EventId: cycleEventPublicID},
	))
	if err != nil {
		t.Fatalf("GetEventOverview() error = %v", err)
	}

	snapshot := overview.Msg.GetOverview().GetSnapshot()
	if snapshot.GetSnapshotId() != optimize.GetObservations().GetSnapshotRef() {
		t.Errorf("overview snapshot = %q, want the one sent to Optimize (%q)",
			snapshot.GetSnapshotId(), optimize.GetObservations().GetSnapshotRef())
	}

	routes := snapshot.GetRouteScores()
	if len(routes) != 1 || routes[0].GetRouteId() != "r1" ||
		routes[0].GetDirectionalFlow().GetForward() != 7 || routes[0].GetDirectionalFlow().GetBackward() != 3 {
		t.Errorf("overview route scores = %v", routes)
	}

	t.Run("returns the previous detection state to Optimize", func(t *testing.T) {
		report(t)

		requests, _ := flow.flow.received()
		if got := requests[len(requests)-1].GetDetectionState().GetConsecutiveSkipCount(); got != 1 {
			t.Errorf("detection_state.consecutive_skip_count = %d, want 1 from the previous response", got)
		}
	})

	t.Run("sends past snapshots as the window series", func(t *testing.T) {
		requests, _ := flow.flow.received()
		latest := requests[len(requests)-1]

		series := latest.GetHistoryDigest().GetWindowSeries()
		if len(series) != 1 || series[0].GetEdgeId() != "r1" {
			t.Fatalf("window series = %v, want one series for r1", series)
		}

		flows := series[0].GetFlowSamples()
		stagnations := series[0].GetStagnationSamples()

		if len(flows) != 1 || flows[0].GetValue() != 10 || len(stagnations) != 1 || stagnations[0].GetValue() != 2.5 {
			t.Errorf("window series samples = %v / %v, want the first cycle's flow 10 and stagnation 2.5", flows, stagnations)
		}

		if !flows[0].GetAt().AsTime().Equal(optimize.GetObservations().GetObservedAt().AsTime()) {
			t.Errorf("window series sample at %v, want the first cycle's %v",
				flows[0].GetAt().AsTime(), optimize.GetObservations().GetObservedAt().AsTime())
		}
	})

	t.Run("accepts measurements while Flow Control is down", func(t *testing.T) {
		flow.flow.setUnavailable(true)
		t.Cleanup(func() { flow.flow.setUnavailable(false) })

		report(t)
	})

	t.Run("answers an event without snapshots with an empty overview", func(t *testing.T) {
		overview, err := flow.statusQueries.GetEventOverview(ctx, authorized(
			flow.token(t, otherEventPublicID, "events.read"),
			&observationv1.GetEventOverviewRequest{EventId: otherEventPublicID},
		))
		if err != nil {
			t.Fatalf("GetEventOverview() error = %v", err)
		}

		if overview.Msg.GetOverview().GetSnapshot() != nil {
			t.Errorf("snapshot = %v, want none", overview.Msg.GetOverview().GetSnapshot())
		}
	})
}

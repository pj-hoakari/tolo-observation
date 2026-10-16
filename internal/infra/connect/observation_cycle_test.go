package connect

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/proto"

	graphv1 "github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1"
	"github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1/graphv1connect"
	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1/flowv1connect"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

const cycleEventPublicID = "1234abcd5678ef90"

type graphStub struct {
	graphv1connect.UnimplementedGraphSupplyServiceHandler

	mu             sync.Mutex
	mappings       []*graphv1.ObservationPointMapping
	qrLocations    []*graphv1.QrLocation
	qrTenantIDs    []string
	authorizations []string
}

func startGraphStub(t *testing.T) (*graphStub, string) {
	t.Helper()

	stub := &graphStub{}
	mux := http.NewServeMux()
	srv := connectrpc.NewServer()
	graphv1connect.RegisterGraphSupplyServiceHandler(srv, stub)
	connecthttp.Mount(mux, srv)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return stub, server.URL
}

func (s *graphStub) mapTo(observationPointID string, anchor *kernelv1.GraphAnchor) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mappings = append(s.mappings, &graphv1.ObservationPointMapping{ObservationPointId: observationPointID, Anchor: anchor})
}

func (s *graphStub) unmap(observationPointID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mappings = slices.DeleteFunc(s.mappings, func(mapping *graphv1.ObservationPointMapping) bool {
		return mapping.GetObservationPointId() == observationPointID
	})
}

func (s *graphStub) GetCurrentRevision(
	ctx context.Context,
	req *graphv1.GetCurrentRevisionRequest,
) (*kernelv1.Graph, error) {
	s.mu.Lock()
	info, _ := connectrpc.CallInfoForServerContext(ctx)
	s.authorizations = append(s.authorizations, info.RequestHeader().Get("Authorization"))
	s.mu.Unlock()

	return &kernelv1.Graph{
		EventId:    req.GetEventId(),
		RevisionId: "rev-1",
		Points: []*kernelv1.Point{
			{PointId: "p1", Type: kernelv1.PointType_POINT_TYPE_GOAL},
			{PointId: "p2", Type: kernelv1.PointType_POINT_TYPE_TRANSIT_ONLY},
			{PointId: "p3", Type: kernelv1.PointType_POINT_TYPE_TRANSIT_ONLY, Boundary: &kernelv1.Boundary{Direction: kernelv1.BoundaryDirection_BOUNDARY_DIRECTION_ENTRY, Active: true}},
			{PointId: "p4", Type: kernelv1.PointType_POINT_TYPE_TRANSIT_ONLY, Boundary: &kernelv1.Boundary{Direction: kernelv1.BoundaryDirection_BOUNDARY_DIRECTION_EXIT, Active: false}},
			{PointId: "p5", Type: kernelv1.PointType_POINT_TYPE_GOAL, Boundary: &kernelv1.Boundary{Direction: kernelv1.BoundaryDirection_BOUNDARY_DIRECTION_ENTRY_AND_EXIT, Active: true}},
		},
		Routes: []*kernelv1.Route{
			{RouteId: "r1", FromPointId: "p1", ToPointId: "p2", Direction: kernelv1.DirectionAttribute_DIRECTION_ATTRIBUTE_BOTH_WAYS},
			{RouteId: "r2", FromPointId: "p2", ToPointId: "p3", Direction: kernelv1.DirectionAttribute_DIRECTION_ATTRIBUTE_BOTH_WAYS},
		},
	}, nil
}

func (s *graphStub) GetObservationPointMappings(
	_ context.Context,
	req *graphv1.GetMappingsRequest,
) (*graphv1.GetMappingsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.GetRevisionId() != "rev-1" {
		return nil, connectrpc.NewError(connectrpc.CodeNotFound, "revision not found")
	}

	return &graphv1.GetMappingsResponse{RevisionId: "rev-1", Mappings: s.mappings}, nil
}

func (s *graphStub) placeQr(qrLocationID string, anchor *kernelv1.GraphAnchor) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.qrLocations = append(s.qrLocations, &graphv1.QrLocation{QrLocationId: qrLocationID, Name: qrLocationID, Kind: "guide", Anchor: anchor})
}

func (s *graphStub) GetQrLocations(
	_ context.Context,
	req *graphv1.GetQrLocationsRequest,
) (*graphv1.GetQrLocationsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.qrTenantIDs = append(s.qrTenantIDs, req.GetTenantId())

	if tenantID := req.GetTenantId(); tenantID != "" && tenantID != testTenantPublicID {
		return nil, connectrpc.NewError(connectrpc.CodePermissionDenied, "revision belongs to another tenant")
	}

	if req.GetRevisionId() != "rev-1" {
		return nil, connectrpc.NewError(connectrpc.CodeNotFound, "revision not found")
	}

	return &graphv1.GetQrLocationsResponse{RevisionId: "rev-1", QrLocations: s.qrLocations}, nil
}

func (s *graphStub) receivedQrTenantIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.qrTenantIDs)
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
	srv := connectrpc.NewServer()
	flowv1connect.RegisterFlowControlServiceHandler(srv, stub)
	connecthttp.Mount(mux, srv)

	server := httptest.NewUnstartedServer(mux)
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	t.Cleanup(server.Close)

	return stub, server.URL
}

func (s *flowStub) Optimize(
	ctx context.Context,
	req *flowv1.OptimizeRequest,
) (*flowv1.OptimizeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.unavailable {
		return nil, connectrpc.NewError(connectrpc.CodeUnavailable, "flow control is down")
	}

	info, _ := connectrpc.CallInfoForServerContext(ctx)
	if info.Protocol != connectrpc.ProtocolNameGRPC {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument, "flow control serves gRPC only")
	}

	s.requests = append(s.requests, req)
	s.authorizations = append(s.authorizations, info.RequestHeader().Get("Authorization"))

	return &flowv1.OptimizeResponse{
		RequestId: proto.String(req.GetRequestId()),
		Verdict:   flowv1.Verdict_VERDICT_SKIPPED_NO_TRIGGER.Enum(),
		UpdatedDetectionState: &flowv1.DetectionState{
			ConsecutiveSkipCount: int32(len(s.requests)),
		},
	}, nil
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

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
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

	deviceID := registered.GetDevice().GetEdgeDeviceId()
	corridor := registered.GetDevice().GetObservationPoints()[0].GetObservationPointId()
	hall := registered.GetDevice().GetObservationPoints()[1].GetObservationPointId()

	flow.graph.mapTo(corridor, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})
	flow.graph.mapTo(hall, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p2"}})

	reportToken := flow.token(t, cycleEventPublicID, "events.report")

	if _, err := flow.edgeDevices.Heartbeat(authorized(ctx, reportToken, &observationv1.HeartbeatRequest{
		EventId:                   cycleEventPublicID,
		EdgeDeviceId:              deviceID,
		ActiveObservationPointIds: []string{corridor, hall},
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	report := func(t *testing.T) {
		t.Helper()

		reported, err := flow.measurements.ReportMeasurements(authorized(ctx, reportToken, &observationv1.ReportMeasurementsRequest{
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

		if got, want := reported.GetAcceptedCount(), int32(2); got != want {
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

	if got := optimize.GetEventContext().GetTerm(); got != flowv1.EventTerm_EVENT_TERM_SHORT {
		t.Errorf("Optimize event_context.term = %v, want short for an event without settings", got)
	}

	if got := len(optimize.GetGraph().GetNodes()) + len(optimize.GetGraph().GetEdges()); got != 7 {
		t.Errorf("Optimize graph has %d elements, want 7", got)
	}

	type nodeBoundary struct {
		boundary  bool
		direction flowv1.BoundaryDirection
		active    bool
		enabled   bool
	}

	gotNodes := map[string]nodeBoundary{}
	for _, node := range optimize.GetGraph().GetNodes() {
		gotNodes[node.GetNodeId()] = nodeBoundary{
			boundary:  node.GetBoundary() != nil,
			direction: node.GetBoundary().GetDirection(),
			active:    node.GetBoundary().GetActive(),
			enabled:   node.GetEnabled(),
		}
	}

	wantNodes := map[string]nodeBoundary{
		"p1": {enabled: true},
		"p2": {enabled: true},
		"p3": {boundary: true, direction: flowv1.BoundaryDirection_BOUNDARY_DIRECTION_ENTRY, active: true, enabled: true},
		"p4": {boundary: true, direction: flowv1.BoundaryDirection_BOUNDARY_DIRECTION_EXIT, active: false, enabled: true},
		"p5": {boundary: true, direction: flowv1.BoundaryDirection_BOUNDARY_DIRECTION_ENTRY_AND_EXIT, active: true, enabled: true},
	}
	if !maps.Equal(gotNodes, wantNodes) {
		t.Errorf("Optimize node boundaries = %+v, want %+v", gotNodes, wantNodes)
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
		stagnations[0].GetDerivation() != flowv1.StagnationDerivation_STAGNATION_DERIVATION_BASIC ||
		!stagnations[0].GetSameSensorFlow() {
		t.Errorf("Optimize arc stagnations = %v, want 2.5 detected people on r1 from the observation point counting its flow", stagnations)
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

	overview, err := flow.statusQueries.GetEventOverview(authorized(ctx,
		flow.token(t, cycleEventPublicID, "events.read"),
		&observationv1.GetEventOverviewRequest{EventId: cycleEventPublicID},
	))
	if err != nil {
		t.Fatalf("GetEventOverview() error = %v", err)
	}

	snapshot := overview.GetOverview().GetSnapshot()
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
		overview, err := flow.statusQueries.GetEventOverview(authorized(ctx,
			flow.token(t, otherEventPublicID, "events.read"),
			&observationv1.GetEventOverviewRequest{EventId: otherEventPublicID},
		))
		if err != nil {
			t.Fatalf("GetEventOverview() error = %v", err)
		}

		if overview.GetOverview().GetSnapshot() != nil {
			t.Errorf("snapshot = %v, want none", overview.GetOverview().GetSnapshot())
		}
	})

	t.Run("sends the long-term category once the event is set to long-term", func(t *testing.T) {
		if _, err := flow.settings.UpdateObservationSettings(authorized(ctx,
			flow.token(t, cycleEventPublicID, "events.manage"),
			&observationv1.UpdateObservationSettingsRequest{
				EventId:  cycleEventPublicID,
				Settings: &observationv1.ObservationSettings{HistoryWindowDays: 30, EventType: observationv1.EventType_EVENT_TYPE_LONG_TERM},
			},
		)); err != nil {
			t.Fatalf("UpdateObservationSettings() error = %v", err)
		}

		report(t)

		requests, _ := flow.flow.received()
		if got := requests[len(requests)-1].GetEventContext().GetTerm(); got != flowv1.EventTerm_EVENT_TERM_LONG {
			t.Errorf("Optimize event_context.term = %v, want long", got)
		}
	})
}

func TestObservationCycleConfidenceFlags(t *testing.T) {
	const eventID = "c0ffee00c0ffee00"

	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               eventID,
			Name:                  "gate-1",
			ObservationPointNames: []string{"corridor", "hall"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	deviceID := registered.GetDevice().GetEdgeDeviceId()
	corridor := registered.GetDevice().GetObservationPoints()[0].GetObservationPointId()
	hall := registered.GetDevice().GetObservationPoints()[1].GetObservationPointId()

	flow.graph.mapTo(corridor, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})
	flow.graph.mapTo(hall, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p2"}})

	reportToken := flow.token(t, eventID, "events.report")

	if _, err := flow.edgeDevices.Heartbeat(authorized(ctx, reportToken, &observationv1.HeartbeatRequest{
		EventId:                   eventID,
		EdgeDeviceId:              deviceID,
		ActiveObservationPointIds: []string{corridor, hall},
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	cycle := func(t *testing.T, corridorEnd *time.Time) *flowv1.OptimizeRequest {
		t.Helper()

		measurements := []*observationv1.Measurement{edgeMeasurement(hall, time.Now().Add(-time.Minute))}

		if corridorEnd != nil {
			corridorMeasurement := edgeMeasurement(corridor, corridorEnd.Add(-time.Minute))
			corridorMeasurement.CountIn = 12
			corridorMeasurement.CountOut = 4
			corridorMeasurement.MeanDetectedPeople = proto.Float64(6)
			measurements = append(measurements, corridorMeasurement)
		}

		if _, err := flow.measurements.ReportMeasurements(authorized(ctx, reportToken, &observationv1.ReportMeasurementsRequest{
			EventId:      eventID,
			EdgeDeviceId: deviceID,
			Measurements: measurements,
		})); err != nil {
			t.Fatalf("ReportMeasurements() error = %v", err)
		}

		requests, _ := flow.flow.received()

		return requests[len(requests)-1]
	}

	arc := func(request *flowv1.OptimizeRequest) string {
		got := ""

		for _, flow := range request.GetObservations().GetArcFlows() {
			if flow.GetEdgeId() == "r1" {
				got += fmt.Sprintf("%v:%v/%v ", flow.GetDirection(), flow.GetFlowRate(), flow.GetConfidenceFlag())
			}
		}

		for _, stagnation := range request.GetObservations().GetArcStagnations() {
			if stagnation.GetEdgeId() == "r1" {
				got += fmt.Sprintf("stagnation:%v/%v", stagnation.GetStagnation(), stagnation.GetConfidenceFlag())
			}
		}

		return got
	}

	r1Series := func(request *flowv1.OptimizeRequest) []*flowv1.ArcWindowSeries {
		return slices.DeleteFunc(request.GetHistoryDigest().GetWindowSeries(), func(series *flowv1.ArcWindowSeries) bool {
			return series.GetEdgeId() != "r1"
		})
	}

	hallOK := func(t *testing.T, request *flowv1.OptimizeRequest) {
		t.Helper()

		occupancies := request.GetObservations().GetNodeOccupancies()
		if len(occupancies) != 1 || occupancies[0].GetNodeId() != "p2" ||
			occupancies[0].GetConfidenceFlag() != flowv1.ConfidenceFlag_CONFIDENCE_FLAG_OK {
			t.Errorf("node occupancies = %v, want p2 observed with OK", occupancies)
		}
	}

	t.Run("marks a route without a measurement for more than 15 minutes INVALID", func(t *testing.T) {
		expired := time.Now().Add(-domain.MaxHold - time.Minute)
		request := cycle(t, &expired)

		want := "FLOW_DIRECTION_A_TO_B:0/CONFIDENCE_FLAG_INVALID FLOW_DIRECTION_B_TO_A:0/CONFIDENCE_FLAG_INVALID "
		if got := arc(request); got != want {
			t.Errorf("r1 observations = %q, want %q", got, want)
		}

		hallOK(t, request)
	})

	t.Run("holds the last valid value of a route silent for up to 15 minutes", func(t *testing.T) {
		recent := time.Now().Add(-5 * time.Minute)
		request := cycle(t, &recent)

		want := "FLOW_DIRECTION_A_TO_B:12/CONFIDENCE_FLAG_HOLD FLOW_DIRECTION_B_TO_A:4/CONFIDENCE_FLAG_HOLD " +
			"stagnation:6/CONFIDENCE_FLAG_HOLD"
		if got := arc(request); got != want {
			t.Errorf("r1 observations = %q, want %q", got, want)
		}

		hallOK(t, request)
	})

	t.Run("keeps held values out of the window series", func(t *testing.T) {
		request := cycle(t, nil)

		if got := arc(request); !strings.Contains(got, "CONFIDENCE_FLAG_HOLD") {
			t.Errorf("r1 observations = %q, want it still held", got)
		}

		if series := r1Series(request); len(series) != 0 {
			t.Errorf("r1 window series = %v, want no samples from held cycles", series)
		}
	})

	t.Run("sends a fresh route as OK and samples it from then on", func(t *testing.T) {
		now := time.Now()

		want := "FLOW_DIRECTION_A_TO_B:12/CONFIDENCE_FLAG_OK FLOW_DIRECTION_B_TO_A:4/CONFIDENCE_FLAG_OK " +
			"stagnation:6/CONFIDENCE_FLAG_OK"
		if got := arc(cycle(t, &now)); got != want {
			t.Errorf("r1 observations = %q, want %q", got, want)
		}

		series := r1Series(cycle(t, nil))
		if len(series) != 1 || len(series[0].GetFlowSamples()) != 1 || series[0].GetFlowSamples()[0].GetValue() != 16 {
			t.Errorf("r1 window series = %v, want the single OK sample 16", series)
		}
	})

	t.Run("marks the anchors of an unregistered device INVALID", func(t *testing.T) {
		other, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
			flow.token(t, eventID, "events.manage"),
			&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "gate-2", ObservationPointNames: []string{"lobby"}},
		))
		if err != nil {
			t.Fatalf("RegisterEdgeDevice() error = %v", err)
		}

		otherID := other.GetDevice().GetEdgeDeviceId()
		lobby := other.GetDevice().GetObservationPoints()[0].GetObservationPointId()
		flow.graph.mapTo(lobby, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p1"}})

		if _, err := flow.edgeDevices.Heartbeat(authorized(ctx, reportToken, &observationv1.HeartbeatRequest{
			EventId: eventID, EdgeDeviceId: otherID, ActiveObservationPointIds: []string{lobby},
		})); err != nil {
			t.Fatalf("Heartbeat() error = %v", err)
		}

		if _, err := flow.edgeDevices.UnregisterEdgeDevice(authorized(ctx,
			flow.token(t, eventID, "events.manage"),
			&observationv1.UnregisterEdgeDeviceRequest{EventId: eventID, EdgeDeviceId: deviceID},
		)); err != nil {
			t.Fatalf("UnregisterEdgeDevice() error = %v", err)
		}

		if _, err := flow.measurements.ReportMeasurements(authorized(ctx, reportToken, &observationv1.ReportMeasurementsRequest{
			EventId:      eventID,
			EdgeDeviceId: otherID,
			Measurements: []*observationv1.Measurement{edgeMeasurement(lobby, time.Now().Add(-time.Minute))},
		})); err != nil {
			t.Fatalf("ReportMeasurements() error = %v", err)
		}

		requests, _ := flow.flow.received()
		request := requests[len(requests)-1]

		got := map[string]flowv1.ConfidenceFlag{}
		for _, occupancy := range request.GetObservations().GetNodeOccupancies() {
			got[occupancy.GetNodeId()] = occupancy.GetConfidenceFlag()
		}

		want := map[string]flowv1.ConfidenceFlag{
			"p1": flowv1.ConfidenceFlag_CONFIDENCE_FLAG_OK,
			"p2": flowv1.ConfidenceFlag_CONFIDENCE_FLAG_INVALID,
		}
		if !maps.Equal(got, want) {
			t.Errorf("node occupancy flags = %v, want %v", got, want)
		}

		wantArc := "FLOW_DIRECTION_A_TO_B:0/CONFIDENCE_FLAG_INVALID FLOW_DIRECTION_B_TO_A:0/CONFIDENCE_FLAG_INVALID "
		if got := arc(request); got != wantArc {
			t.Errorf("r1 observations = %q, want %q", got, wantArc)
		}
	})
}

func TestObservationCycleSensorSetChanges(t *testing.T) {
	const eventID = "5e4505e7c4a49e00"

	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               eventID,
			Name:                  "gate-1",
			ObservationPointNames: []string{"corridor", "aisle", "hall"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	deviceID := registered.GetDevice().GetEdgeDeviceId()
	points := registered.GetDevice().GetObservationPoints()
	corridor, aisle, hall := points[0].GetObservationPointId(), points[1].GetObservationPointId(), points[2].GetObservationPointId()
	r1 := &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}}

	flow.graph.mapTo(corridor, r1)
	flow.graph.mapTo(hall, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p2"}})
	flow.graph.mapTo(aisle, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r9"}})

	reportToken := flow.token(t, eventID, "events.report")

	if _, err := flow.edgeDevices.Heartbeat(authorized(ctx, reportToken, &observationv1.HeartbeatRequest{
		EventId:                   eventID,
		EdgeDeviceId:              deviceID,
		ActiveObservationPointIds: []string{corridor, aisle, hall},
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	report := func(t *testing.T, windowStart time.Time) {
		t.Helper()

		if _, err := flow.measurements.ReportMeasurements(authorized(ctx, reportToken, &observationv1.ReportMeasurementsRequest{
			EventId:      eventID,
			EdgeDeviceId: deviceID,
			Measurements: []*observationv1.Measurement{
				edgeMeasurement(corridor, windowStart),
				edgeMeasurement(aisle, windowStart),
				edgeMeasurement(hall, windowStart),
			},
		})); err != nil {
			t.Fatalf("ReportMeasurements() error = %v", err)
		}
	}

	cycle := func(t *testing.T) *flowv1.OptimizeRequest {
		t.Helper()

		report(t, time.Now().Add(-time.Minute))

		requests, _ := flow.flow.received()

		return requests[len(requests)-1]
	}

	events := func(request *flowv1.OptimizeRequest) []string {
		var got []string
		for _, event := range request.GetEvents() {
			got = append(got, fmt.Sprintf("%v %s", event.GetKind(), event.GetTargetId()))
		}

		return got
	}

	r1Flows := func(request *flowv1.OptimizeRequest) []float64 {
		var got []float64

		for _, series := range request.GetHistoryDigest().GetWindowSeries() {
			if series.GetEdgeId() == "r1" {
				for _, sample := range series.GetFlowSamples() {
					got = append(got, sample.GetValue())
				}
			}
		}

		return got
	}

	r1Stat := func(request *flowv1.OptimizeRequest) *flowv1.ArcHistoryStat {
		for _, stat := range request.GetHistoryDigest().GetArcStats() {
			if stat.GetEdgeId() == "r1" {
				return stat
			}
		}

		return nil
	}

	t.Run("sends no event for anchors observed for the first time", func(t *testing.T) {
		if got := events(cycle(t)); len(got) != 0 {
			t.Errorf("events = %v, want none", got)
		}

		request := cycle(t)
		if got := events(request); len(got) != 0 {
			t.Errorf("events = %v, want none", got)
		}

		if got := r1Flows(request); !slices.Equal(got, []float64{10}) {
			t.Errorf("r1 flow samples = %v, want the one sample from the corridor", got)
		}

		if r1Stat(request) == nil {
			t.Errorf("r1 arc stat is missing, want the stored corridor history")
		}
	})

	var changedAt time.Time

	t.Run("sends SENSOR_SET_CHANGED for a route that gains an observation point and drops its history", func(t *testing.T) {
		flow.graph.unmap(aisle)
		flow.graph.mapTo(aisle, r1)

		request := cycle(t)

		want := []string{"EVENT_KIND_SENSOR_SET_CHANGED edge:r1"}
		if got := events(request); !slices.Equal(got, want) {
			t.Fatalf("events = %v, want %v", got, want)
		}

		changedAt = request.GetEvents()[0].GetOccurredAt().AsTime()
		if serverTime := request.GetServerTime().AsTime(); !changedAt.Equal(serverTime) {
			t.Errorf("occurred_at = %v, want the server time %v", changedAt, serverTime)
		}

		if got := r1Flows(request); len(got) != 0 {
			t.Errorf("r1 flow samples = %v, want none from before the change", got)
		}

		if stat := r1Stat(request); stat != nil {
			t.Errorf("r1 arc stat = %v, want none across the change", stat)
		}
	})

	t.Run("does not repeat the event once Flow Control received it", func(t *testing.T) {
		request := cycle(t)
		if got := events(request); len(got) != 0 {
			t.Errorf("events = %v, want none", got)
		}

		if got := r1Flows(request); !slices.Equal(got, []float64{20}) {
			t.Errorf("r1 flow samples = %v, want only the corridor and aisle sample from the change on", got)
		}
	})

	t.Run("keeps a route recovering from INVALID with the same points continuous", func(t *testing.T) {
		setEnabled := func(t *testing.T, enabled bool) {
			t.Helper()

			if _, err := flow.edgeDevices.UpdateObservationPointConfig(authorized(ctx,
				flow.token(t, eventID, "events.operate"),
				&observationv1.UpdateObservationPointConfigRequest{
					EventId: eventID, ObservationPointId: corridor, Name: "corridor", Enabled: enabled,
				},
			)); err != nil {
				t.Fatalf("UpdateObservationPointConfig() error = %v", err)
			}
		}

		setEnabled(t, false)

		request := cycle(t)
		if flows := request.GetObservations().GetArcFlows(); len(flows) == 0 || flows[0].GetEdgeId() != "r1" ||
			flows[0].GetConfidenceFlag() != flowv1.ConfidenceFlag_CONFIDENCE_FLAG_INVALID {
			t.Fatalf("arc flows = %v, want r1 INVALID while the corridor is disabled", flows)
		}

		setEnabled(t, true)

		if got := events(cycle(t)); len(got) != 0 {
			t.Errorf("events = %v, want none for a recovery with the same observation points", got)
		}
	})

	t.Run("resends the event until Flow Control receives it", func(t *testing.T) {
		flow.flow.setUnavailable(true)
		flow.graph.unmap(aisle)
		report(t, time.Now().Add(-time.Minute))
		flow.flow.setUnavailable(false)

		request := cycle(t)

		want := []string{"EVENT_KIND_SENSOR_SET_CHANGED edge:r1"}
		if got := events(request); !slices.Equal(got, want) {
			t.Fatalf("events = %v, want %v", got, want)
		}

		if at := request.GetEvents()[0].GetOccurredAt().AsTime(); !at.After(changedAt) || !at.Before(request.GetServerTime().AsTime()) {
			t.Errorf("occurred_at = %v, want the time of the failed cycle that detected the change", at)
		}

		if got := events(cycle(t)); len(got) != 0 {
			t.Errorf("events = %v, want none", got)
		}
	})
}

func TestObservationCycleQRObservationPoints(t *testing.T) {
	const eventID = "6f5616f8d5b5af11"

	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "gate-1", ObservationPointNames: []string{"corridor"}},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	deviceID := registered.GetDevice().GetEdgeDeviceId()
	corridor := registered.GetDevice().GetObservationPoints()[0].GetObservationPointId()
	r2 := &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r2"}}

	flow.graph.mapTo(corridor, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})
	flow.graph.placeQr("qr-stairs", r2)

	reportToken := flow.token(t, eventID, "events.report")

	if _, err := flow.edgeDevices.Heartbeat(authorized(ctx, reportToken, &observationv1.HeartbeatRequest{
		EventId: eventID, EdgeDeviceId: deviceID, ActiveObservationPointIds: []string{corridor},
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	cycle := func(t *testing.T) *flowv1.OptimizeRequest {
		t.Helper()

		if _, err := flow.measurements.ReportMeasurements(authorized(ctx, reportToken, &observationv1.ReportMeasurementsRequest{
			EventId:      eventID,
			EdgeDeviceId: deviceID,
			Measurements: []*observationv1.Measurement{edgeMeasurement(corridor, time.Now().Add(-time.Minute))},
		})); err != nil {
			t.Fatalf("ReportMeasurements() error = %v", err)
		}

		requests, _ := flow.flow.received()
		if len(requests) == 0 {
			t.Fatalf("Optimize was not called")
		}

		return requests[len(requests)-1]
	}

	observedEdges := func(request *flowv1.OptimizeRequest) []string {
		var got []string
		for _, arc := range request.GetObservations().GetArcFlows() {
			got = append(got, fmt.Sprintf("%s/%v", arc.GetEdgeId(), arc.GetConfidenceFlag()))
		}

		return got
	}

	t.Run("runs the cycle with a silent QR observation point and sends Flow Control only the camera route", func(t *testing.T) {
		request := cycle(t)

		want := []string{"r1/CONFIDENCE_FLAG_OK", "r1/CONFIDENCE_FLAG_OK"}
		if got := observedEdges(request); !slices.Equal(got, want) {
			t.Errorf("arc flows = %v, want %v", got, want)
		}

		if events := request.GetEvents(); len(events) != 0 {
			t.Errorf("events = %v, want none", events)
		}
	})

	t.Run("asks Graph Authoring for the QR locations of the event's tenant", func(t *testing.T) {
		cycle(t)

		got := flow.graph.receivedQrTenantIDs()
		if len(got) == 0 || got[len(got)-1] != testTenantPublicID {
			t.Errorf("GetQrLocations tenant_ids = %v, want the last one %q", got, testTenantPublicID)
		}
	})

	t.Run("sends SENSOR_SET_CHANGED when a QR location joins a route", func(t *testing.T) {
		flow.graph.placeQr("qr-landing", r2)

		var got []string
		for _, event := range cycle(t).GetEvents() {
			got = append(got, fmt.Sprintf("%v %s", event.GetKind(), event.GetTargetId()))
		}

		if want := []string{"EVENT_KIND_SENSOR_SET_CHANGED edge:r2"}; !slices.Equal(got, want) {
			t.Errorf("events = %v, want %v", got, want)
		}
	})
}

func TestObservationCycleOfflineObservationPoints(t *testing.T) {
	const eventID = "c0ffee00c0ffee01"

	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "gate-offline", ObservationPointNames: []string{"hall", "lobby", "gate"}},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	points := registered.GetDevice().GetObservationPoints()
	hall := points[0].GetObservationPointId()
	lobby := points[1].GetObservationPointId()
	gate := points[2].GetObservationPointId()

	flow.graph.mapTo(hall, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p1"}})
	flow.graph.mapTo(lobby, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p2"}})
	flow.graph.mapTo(gate, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p3"}})

	if _, err := flow.edgeDevices.UpdateObservationPointConfig(authorized(ctx,
		flow.token(t, eventID, "events.operate"),
		&observationv1.UpdateObservationPointConfigRequest{EventId: eventID, ObservationPointId: gate, Name: "gate", Enabled: false},
	)); err != nil {
		t.Fatalf("UpdateObservationPointConfig() error = %v", err)
	}

	if _, err := flow.measurements.ReportMeasurements(authorized(ctx,
		flow.token(t, eventID, "events.report"),
		&observationv1.ReportMeasurementsRequest{
			EventId:      eventID,
			EdgeDeviceId: registered.GetDevice().GetEdgeDeviceId(),
			Measurements: []*observationv1.Measurement{
				edgeMeasurement(hall, time.Now().Add(-11*time.Minute)),
				edgeMeasurement(lobby, time.Now().Add(-17*time.Minute)),
				edgeMeasurement(gate, time.Now().Add(-time.Minute)),
			},
		},
	)); err != nil {
		t.Fatalf("ReportMeasurements() error = %v", err)
	}

	requests, _ := flow.flow.received()

	got := map[string]string{}
	for _, occupancy := range requests[len(requests)-1].GetObservations().GetNodeOccupancies() {
		got[occupancy.GetNodeId()] = fmt.Sprintf("%v/occupancy_delta_set=%v/occupancy=%v",
			occupancy.GetConfidenceFlag(), occupancy.OccupancyDelta != nil, occupancy.GetOccupancy())
	}

	want := map[string]string{
		"p1": "CONFIDENCE_FLAG_HOLD/occupancy_delta_set=false/occupancy=2.5",
		"p2": "CONFIDENCE_FLAG_INVALID/occupancy_delta_set=true/occupancy=0",
		"p3": "CONFIDENCE_FLAG_INVALID/occupancy_delta_set=true/occupancy=0",
	}
	if !maps.Equal(got, want) {
		t.Errorf("node occupancies of a device without heartbeats "+
			"(hall 10 min and lobby 16 min since last measurement, gate disabled with a fresh one) = %v, want %v", got, want)
	}
}

func TestEventOverviewShowsAnchorsWithGapsAsMissing(t *testing.T) {
	const eventID = "c0ffee00c0ffee02"

	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId: eventID, Name: "gate-gaps", ObservationPointNames: []string{"door", "window", "hall", "lobby"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	deviceID := registered.GetDevice().GetEdgeDeviceId()
	points := registered.GetDevice().GetObservationPoints()
	door := points[0].GetObservationPointId()
	window := points[1].GetObservationPointId()
	hall := points[2].GetObservationPointId()
	lobby := points[3].GetObservationPointId()

	flow.graph.mapTo(door, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})
	flow.graph.mapTo(window, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})
	flow.graph.mapTo(hall, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p1"}})
	flow.graph.mapTo(lobby, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p2"}})

	reportToken := flow.token(t, eventID, "events.report")

	if _, err := flow.edgeDevices.Heartbeat(authorized(ctx, reportToken, &observationv1.HeartbeatRequest{
		EventId: eventID, EdgeDeviceId: deviceID, ActiveObservationPointIds: []string{door, window, hall, lobby},
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	if _, err := flow.measurements.ReportMeasurements(authorized(ctx, reportToken, &observationv1.ReportMeasurementsRequest{
		EventId:      eventID,
		EdgeDeviceId: deviceID,
		Measurements: []*observationv1.Measurement{
			edgeMeasurement(door, time.Now().Add(-time.Minute)),
			edgeMeasurement(window, time.Now().Add(-5*time.Minute)),
			edgeMeasurement(hall, time.Now().Add(-time.Minute)),
			edgeMeasurement(lobby, time.Now().Add(-domain.MaxHold-2*time.Minute)),
		},
	})); err != nil {
		t.Fatalf("ReportMeasurements() error = %v", err)
	}

	overview, err := flow.statusQueries.GetEventOverview(authorized(ctx,
		flow.token(t, eventID, "events.read"),
		&observationv1.GetEventOverviewRequest{EventId: eventID},
	))
	if err != nil {
		t.Fatalf("GetEventOverview() error = %v", err)
	}

	snapshot := overview.GetOverview().GetSnapshot()

	routes := map[string]*kernelv1.RouteScore{}
	for _, route := range snapshot.GetRouteScores() {
		routes[route.GetRouteId()] = route
	}

	pointScores := map[string]*kernelv1.PointScore{}
	for _, point := range snapshot.GetPointScores() {
		pointScores[point.GetPointId()] = point
	}

	if got, want := routes["r1"], (&kernelv1.RouteScore{RouteId: "r1"}); len(routes) != 1 || !proto.Equal(got, want) {
		t.Errorf("route scores = %v, want only r1 (door fresh, window held) as missing %v", routes, want)
	}

	if got, want := pointScores["p2"], (&kernelv1.PointScore{PointId: "p2"}); !proto.Equal(got, want) {
		t.Errorf("p2 (lobby silent past the hold) = %v, want missing %v", got, want)
	}

	want := &kernelv1.PointScore{PointId: "p1", PeopleScore: proto.Float64(2.5), OccupancyDelta: proto.Float64(4), Exhaustive: true}
	if got := pointScores["p1"]; len(pointScores) != 2 || !proto.Equal(got, want) {
		t.Errorf("point scores = %v, want p1 %v next to the missing p2", pointScores, want)
	}
}

func TestReportQRMeasurements(t *testing.T) {
	const (
		eventID     = "6f5616f8d5b5af12"
		otherTenant = "ffffeeeeddddcccc"
		stairs      = "0a0a0a0a0a0a0a01"
		landing     = "0a0a0a0a0a0a0a02"
		unplaced    = "0a0a0a0a0a0a0a03"
	)

	flow := newObservationFlow(t)
	ctx := context.Background()

	flow.graph.placeQr(stairs, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r2"}})
	flow.graph.placeQr(landing, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_PointId{PointId: "p3"}})

	qrMeasurement := func(qrLocationID string) *observationv1.Measurement {
		measurement := edgeMeasurement("", time.Now().Add(-time.Minute))
		measurement.Source = observationv1.MeasurementSource_MEASUREMENT_SOURCE_QR
		measurement.QrLocationId = qrLocationID
		measurement.MeanDetectedPeople = nil

		return measurement
	}

	report := func(authorization string, request *observationv1.ReportMeasurementsRequest) error {
		_, err := flow.measurements.ReportMeasurements(authorized(ctx, authorization, request))

		return err
	}

	t.Run("accepts QR counts from the Guest Service and scores them without a direction", func(t *testing.T) {
		before := len(flow.graph.receivedQrTenantIDs())

		if err := report(flow.serviceToken(t, "", ""), &observationv1.ReportMeasurementsRequest{
			TenantId:     testTenantPublicID,
			EventId:      eventID,
			Measurements: []*observationv1.Measurement{qrMeasurement(stairs), qrMeasurement(landing)},
		}); err != nil {
			t.Fatalf("ReportMeasurements() error = %v", err)
		}

		if got := flow.graph.receivedQrTenantIDs()[before:]; len(got) == 0 || got[0] != testTenantPublicID {
			t.Errorf("GetQrLocations tenant_ids = %v, want the request's tenant %q first", got, testTenantPublicID)
		}

		requests, _ := flow.flow.received()
		if observations := requests[len(requests)-1].GetObservations(); len(observations.GetArcFlows())+len(observations.GetNodeOccupancies()) != 0 {
			t.Errorf("flow observations = %v, want QR scores kept out of Flow Control", observations)
		}

		overview, err := flow.statusQueries.GetEventOverview(authorized(ctx,
			flow.token(t, eventID, "events.read"),
			&observationv1.GetEventOverviewRequest{EventId: eventID},
		))
		if err != nil {
			t.Fatalf("GetEventOverview() error = %v", err)
		}

		snapshot := overview.GetOverview().GetSnapshot()

		var got []string
		for _, route := range snapshot.GetRouteScores() {
			got = append(got, fmt.Sprintf("%s %v exhaustive=%v", route.GetRouteId(), route.GetFlow(), route.GetExhaustive()))
		}

		for _, point := range snapshot.GetPointScores() {
			got = append(got, fmt.Sprintf("%s people_set=%v delta=%v exhaustive=%v",
				point.GetPointId(), point.PeopleScore != nil, point.GetOccupancyDelta(), point.GetExhaustive()))
		}

		want := []string{
			fmt.Sprintf("r2 %v exhaustive=false", &kernelv1.RouteScore_ScalarFlow{ScalarFlow: 7}),
			"p3 people_set=false delta=7 exhaustive=false",
		}
		if !slices.Equal(got, want) {
			t.Errorf("overview scores = %v, want %v", got, want)
		}
	})

	tests := []struct {
		name          string
		authorization func(t *testing.T) string
		request       *observationv1.ReportMeasurementsRequest
		want          connectrpc.Code
	}{
		{
			name:          "refuses a QR location outside the current revision",
			authorization: func(t *testing.T) string { return flow.serviceToken(t, "", "") },
			request: &observationv1.ReportMeasurementsRequest{
				TenantId: testTenantPublicID, EventId: eventID, Measurements: []*observationv1.Measurement{qrMeasurement(unplaced)},
			},
			want: connectrpc.CodePermissionDenied,
		},
		{
			name:          "refuses a tenant Graph Authoring does not place the event under",
			authorization: func(t *testing.T) string { return flow.serviceToken(t, "", "") },
			request: &observationv1.ReportMeasurementsRequest{
				TenantId: otherTenant, EventId: eventID, Measurements: []*observationv1.Measurement{qrMeasurement(stairs)},
			},
			want: connectrpc.CodePermissionDenied,
		},
		{
			name:          "requires the tenant",
			authorization: func(t *testing.T) string { return flow.serviceToken(t, "", "") },
			request: &observationv1.ReportMeasurementsRequest{
				EventId: eventID, Measurements: []*observationv1.Measurement{qrMeasurement(stairs)},
			},
			want: connectrpc.CodeInvalidArgument,
		},
		{
			name:          "refuses a tenant other than the token's",
			authorization: func(t *testing.T) string { return flow.serviceToken(t, "organizer", testTenantPublicID) },
			request: &observationv1.ReportMeasurementsRequest{
				TenantId: otherTenant, EventId: eventID, Measurements: []*observationv1.Measurement{qrMeasurement(stairs)},
			},
			want: connectrpc.CodePermissionDenied,
		},
		{
			name:          "refuses an event other than the token's",
			authorization: func(t *testing.T) string { return flow.token(t, otherEventPublicID, "events.report") },
			request: &observationv1.ReportMeasurementsRequest{
				TenantId: testTenantPublicID, EventId: eventID, Measurements: []*observationv1.Measurement{qrMeasurement(stairs)},
			},
			want: connectrpc.CodePermissionDenied,
		},
		{
			name:          "refuses an edge device on QR counts",
			authorization: func(t *testing.T) string { return flow.serviceToken(t, "", "") },
			request: &observationv1.ReportMeasurementsRequest{
				TenantId: testTenantPublicID, EventId: eventID, EdgeDeviceId: "1122334455667788",
				Measurements: []*observationv1.Measurement{qrMeasurement(stairs)},
			},
			want: connectrpc.CodeInvalidArgument,
		},
		{
			name:          "refuses a QR count naming an observation point",
			authorization: func(t *testing.T) string { return flow.serviceToken(t, "", "") },
			request: func() *observationv1.ReportMeasurementsRequest {
				measurement := qrMeasurement(stairs)
				measurement.ObservationPointId = stairs

				return &observationv1.ReportMeasurementsRequest{
					TenantId: testTenantPublicID, EventId: eventID, Measurements: []*observationv1.Measurement{measurement},
				}
			}(),
			want: connectrpc.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectrpc.CodeOf(report(tt.authorization(t), tt.request)); got != tt.want {
				t.Errorf("ReportMeasurements() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReportEdgeMeasurementsChecksTheTenant(t *testing.T) {
	const eventID = "6f5616f8d5b5af13"

	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "gate-1", ObservationPointNames: []string{"corridor"}},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	report := func(tenantID string) error {
		_, err := flow.measurements.ReportMeasurements(authorized(ctx,
			flow.token(t, eventID, "events.report"),
			&observationv1.ReportMeasurementsRequest{
				TenantId:     tenantID,
				EventId:      eventID,
				EdgeDeviceId: registered.GetDevice().GetEdgeDeviceId(),
				Measurements: []*observationv1.Measurement{
					edgeMeasurement(registered.GetDevice().GetObservationPoints()[0].GetObservationPointId(), time.Now().Add(-time.Minute)),
				},
			},
		))

		return err
	}

	for _, tenantID := range []string{"", testTenantPublicID} {
		if err := report(tenantID); err != nil {
			t.Errorf("ReportMeasurements(tenant_id=%q) error = %v", tenantID, err)
		}
	}

	if got, want := connectrpc.CodeOf(report("ffffeeeeddddcccc")), connectrpc.CodePermissionDenied; got != want {
		t.Errorf("ReportMeasurements(tenant_id of another tenant) error code = %v, want %v", got, want)
	}
}

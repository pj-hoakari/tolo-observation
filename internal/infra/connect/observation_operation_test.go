package connect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	dbinfra "github.com/pj-hoakari/tolo-observation/internal/infra/db"
	operationv1 "github.com/pj-hoakari/tolo-operation/gen/tolo/operation/v1"
	"github.com/pj-hoakari/tolo-operation/gen/tolo/operation/v1/operationv1connect"
)

const operationTestTimeout = 300 * time.Millisecond

type operationCall struct {
	procedure     string
	authorization string
	request       proto.Message
}

type operationStub struct {
	operationv1connect.UnimplementedDeliveryCoordinationServiceHandler

	mu      sync.Mutex
	calls   []operationCall
	failing map[string]bool
	hanging map[string]bool
}

func startOperationStub(t *testing.T) (*operationStub, string) {
	t.Helper()

	stub := &operationStub{failing: map[string]bool{}, hanging: map[string]bool{}}
	mux := http.NewServeMux()
	srv := connectrpc.NewServer()
	operationv1connect.RegisterDeliveryCoordinationServiceHandler(srv, stub)
	connecthttp.Mount(mux, srv)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return stub, server.URL
}

func (s *operationStub) record(ctx context.Context, procedure string, request proto.Message) error {
	info, _ := connectrpc.CallInfoForServerContext(ctx)

	s.mu.Lock()
	s.calls = append(s.calls, operationCall{procedure, info.RequestHeader().Get("Authorization"), request})
	failing, hanging := s.failing[procedure], s.hanging[procedure]
	s.mu.Unlock()

	if hanging {
		<-ctx.Done()

		return connectrpc.NewError(connectrpc.CodeDeadlineExceeded, "operation is hanging")
	}

	if failing {
		return connectrpc.NewError(connectrpc.CodeUnavailable, "operation is down")
	}

	return nil
}

func (s *operationStub) RequestProposalDelivery(
	ctx context.Context,
	req *operationv1.RequestProposalDeliveryRequest,
) (*operationv1.RequestProposalDeliveryResponse, error) {
	if err := s.record(ctx, "RequestProposalDelivery", req); err != nil {
		return nil, err
	}

	return &operationv1.RequestProposalDeliveryResponse{DeliveryId: "delivery-1", RealtimeCount: 1, NotificationCount: 0}, nil
}

func (s *operationStub) fail(procedure string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failing[procedure] = true
}

func (s *operationStub) hang(procedure string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hanging[procedure] = true
}

func (s *operationStub) received(procedure string) []operationCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	var calls []operationCall

	for _, call := range s.calls {
		if call.procedure == procedure {
			calls = append(calls, call)
		}
	}

	return calls
}

func operationTestResult() *flowv1.OptimizationResult {
	return &flowv1.OptimizationResult{
		RouteImportance: []*flowv1.RouteImportance{
			{EdgeId: "r1", Direction: flowv1.ImportanceDirection_IMPORTANCE_DIRECTION_A_TO_B, Importance: 0.9},
		},
		BoundaryControl: []*flowv1.BoundaryControl{
			{NodeId: "p3", Action: flowv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_INGRESS},
		},
		SolvedAt: timestamppb.New(time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)),
	}
}

func reportForOperation(t *testing.T, flow observationFlow, eventID string) (string, time.Duration) {
	t.Helper()

	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
		flow.token(t, eventID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{EventId: eventID, Name: "gate-operation", ObservationPointNames: []string{"corridor"}},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	corridor := registered.GetDevice().GetObservationPoints()[0].GetObservationPointId()
	flow.graph.mapTo(corridor, &kernelv1.GraphAnchor{Target: &kernelv1.GraphAnchor_RouteId{RouteId: "r1"}})

	reportToken := flow.token(t, eventID, "events.report")
	started := time.Now()

	if _, err := flow.measurements.ReportMeasurements(authorized(ctx, reportToken, &observationv1.ReportMeasurementsRequest{
		EventId:      eventID,
		EdgeDeviceId: registered.GetDevice().GetEdgeDeviceId(),
		Measurements: []*observationv1.Measurement{edgeMeasurement(corridor, flow.observedStart)},
	})); err != nil {
		t.Fatalf("ReportMeasurements() error = %v", err)
	}

	return reportToken, time.Since(started)
}

func TestObservationCycleRequestsProposalDelivery(t *testing.T) {
	const eventID = "0be7a71000000001"

	flow := newObservationFlow(t)
	flow.flow.respondWith(operationTestResult())

	authorization, _ := reportForOperation(t, flow, eventID)

	calls := flow.operation.received("RequestProposalDelivery")
	if len(calls) != 1 {
		t.Fatalf("RequestProposalDelivery received %d calls, want 1", len(calls))
	}

	if calls[0].authorization != authorization {
		t.Errorf("RequestProposalDelivery Authorization = %q, want the token that reported the measurements", calls[0].authorization)
	}

	req, _ := calls[0].request.(*operationv1.RequestProposalDeliveryRequest)
	if req.GetTenantId() != testTenantPublicID || req.GetEventId() != eventID {
		t.Errorf("RequestProposalDelivery tenant/event = %q/%q, want %q/%q", req.GetTenantId(), req.GetEventId(), testTenantPublicID, eventID)
	}

	want := &operationv1.FlowProposals{
		RouteImportances: []*operationv1.RouteImportance{
			{RouteId: "r1", Direction: operationv1.RouteDirection_ROUTE_DIRECTION_FORWARD.Enum(), Importance: 0.9},
		},
		BoundaryControls: []*operationv1.BoundaryControlProposal{
			{PointId: "p3", Action: operationv1.BoundaryAction_BOUNDARY_ACTION_PAUSE_INGRESS},
		},
		SolvedAt: timestamppb.New(time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)),
	}
	if !proto.Equal(req.GetFlowProposals(), want) {
		t.Errorf("RequestProposalDelivery flow_proposals = %v, want %v", req.GetFlowProposals(), want)
	}
}

func TestObservationCycleSkipsProposalDeliveryWithoutProposals(t *testing.T) {
	const eventID = "0be7a71000000002"

	flow := newObservationFlow(t)
	flow.flow.respondWith(&flowv1.OptimizationResult{SolvedAt: timestamppb.Now()})

	reportForOperation(t, flow, eventID)

	if calls := flow.operation.received("RequestProposalDelivery"); len(calls) != 0 {
		t.Errorf("RequestProposalDelivery received %d calls for a result without proposals, want 0", len(calls))
	}
}

func TestObservationCycleKeepsGoingWhenOperationFails(t *testing.T) {
	tests := []struct {
		name    string
		eventID string
		break_  func(stub *operationStub, procedure string)
	}{
		{"unavailable", "0be7a71000000003", (*operationStub).fail},
		{"slower than the operation timeout", "0be7a71000000004", (*operationStub).hang},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := newObservationFlow(t)
			flow.flow.respondWith(operationTestResult())
			tt.break_(flow.operation, "RequestProposalDelivery")

			_, elapsed := reportForOperation(t, flow, tt.eventID)

			if elapsed > operationTestTimeout+2*time.Second {
				t.Errorf("ReportMeasurements took %s, want it bounded by the operation timeout %s", elapsed, operationTestTimeout)
			}

			if calls := flow.operation.received("RequestProposalDelivery"); len(calls) != 1 {
				t.Errorf("RequestProposalDelivery received %d calls, want 1", len(calls))
			}

			outcome, err := dbinfra.NewPostgresSnapshotRepository(testDB).LatestOptimizationOutcome(context.Background(), tt.eventID)
			if err != nil {
				t.Fatalf("LatestOptimizationOutcome() error = %v, want the outcome saved despite Operation failing", err)
			}

			if outcome.OptimizationResult == nil {
				t.Error("saved outcome has no optimization result")
			}
		})
	}
}

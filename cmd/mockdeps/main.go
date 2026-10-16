package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/proto"

	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"
	graphv1 "github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1"
	"github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1/graphv1connect"
	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	flowv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/flow/v1/flowv1connect"
)

const (
	revisionID   = "mock-revision-1"
	qrLocationID = "a1b2c3d4e5f60718"
	routeID      = "route-entrance-hall"
)

func main() {
	if err := run(); err != nil {
		slog.Error("mockdeps failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	generator, err := jwtgen.NewGenerator("")
	if err != nil {
		return fmt.Errorf("create token generator: %w", err)
	}

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           newHandler(generator),
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}

	slog.Info("mockdeps listening", "addr", server.Addr)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func newHandler(generator *jwtgen.Generator) http.Handler {
	logCalls := connectrpc.ServerInterceptor(func(next connectrpc.ServerFunc) connectrpc.ServerFunc {
		return func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
			slog.InfoContext(ctx, "rpc called", "procedure", spec.Procedure)

			return next(ctx, spec, stream)
		}
	})

	mux := http.NewServeMux()
	server := connectrpc.NewServer(logCalls)
	graphv1connect.RegisterGraphSupplyServiceHandler(server, new(graphSupply))
	flowv1connect.RegisterFlowControlServiceHandler(server, flowControl{})
	connecthttp.Mount(mux, server)

	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		jwks, err := generator.JWKS()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		writeJSON(w, jwks)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		config, err := decodeTokenRequest(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		output, err := generator.Generate(config)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		writeJSON(w, output)
	})

	return mux
}

type tokenRequest struct {
	Audience       string `json:"audience"`
	TokenUse       string `json:"token_use"`
	TenantPublicID string `json:"tenant_id"`
	EventPublicID  string `json:"event_id"`
	Scope          string `json:"scope"`
	TTL            string `json:"ttl"`
}

func decodeTokenRequest(body io.Reader) (jwtgen.Config, error) {
	req := tokenRequest{
		Audience:       "tolo-observation",
		TokenUse:       "event_access",
		TenantPublicID: "",
		EventPublicID:  "",
		Scope:          "",
		TTL:            "10m",
	}
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		return jwtgen.Config{}, fmt.Errorf("decode token request: %w", err)
	}

	ttl, err := time.ParseDuration(req.TTL)
	if err != nil {
		return jwtgen.Config{}, fmt.Errorf("parse ttl: %w", err)
	}

	return jwtgen.Config{
		Issuer:         "service-gateway",
		Audience:       req.Audience,
		TokenUse:       req.TokenUse,
		TenantPublicID: req.TenantPublicID,
		EventPublicID:  req.EventPublicID,
		Scope:          req.Scope,
		OriginSub:      "",
		Txn:            "",
		Subject:        "",
		KeyID:          "",
		TTL:            ttl,
	}, nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write response failed", "error", err)
	}
}

type graphSupply struct {
	graphv1connect.UnimplementedGraphSupplyServiceHandler
}

func (graphSupply) GetCurrentRevision(
	_ context.Context,
	req *graphv1.GetCurrentRevisionRequest,
) (*kernelv1.Graph, error) {
	return &kernelv1.Graph{
		EventId:    req.GetEventId(),
		RevisionId: revisionID,
		Points: []*kernelv1.Point{
			{PointId: "point-entrance", Type: kernelv1.PointType_POINT_TYPE_TRANSIT_ONLY, Boundary: nil},
			{PointId: "point-hall", Type: kernelv1.PointType_POINT_TYPE_GOAL, Boundary: nil},
		},
		Routes: []*kernelv1.Route{{
			RouteId:      routeID,
			FromPointId:  "point-entrance",
			ToPointId:    "point-hall",
			Direction:    kernelv1.DirectionAttribute_DIRECTION_ATTRIBUTE_BOTH_WAYS,
			CapacityHint: nil,
		}},
	}, nil
}

func (graphSupply) GetObservationPointMappings(
	context.Context, *graphv1.GetMappingsRequest,
) (*graphv1.GetMappingsResponse, error) {
	return &graphv1.GetMappingsResponse{RevisionId: revisionID, Mappings: nil}, nil
}

func (graphSupply) GetQrLocations(
	context.Context, *graphv1.GetQrLocationsRequest,
) (*graphv1.GetQrLocationsResponse, error) {
	return &graphv1.GetQrLocationsResponse{
		RevisionId: revisionID,
		QrLocations: []*graphv1.QrLocation{{
			QrLocationId: qrLocationID,
			Name:         "Entrance QR",
			Kind:         "guide",
			Anchor: &kernelv1.GraphAnchor{
				Target:        &kernelv1.GraphAnchor_RouteId{RouteId: routeID},
				RoutePosition: nil,
			},
		}},
	}, nil
}

type flowControl struct{}

func (flowControl) Optimize(
	_ context.Context,
	req *flowv1.OptimizeRequest,
) (*flowv1.OptimizeResponse, error) {
	return &flowv1.OptimizeResponse{
		RequestId:             proto.String(req.GetRequestId()),
		Verdict:               flowv1.Verdict_VERDICT_SKIPPED_NO_TRIGGER.Enum(),
		UpdatedDetectionState: new(flowv1.DetectionState),
		FeedbackValues:        nil,
		Diagnostics:           nil,
		OptimizationResult:    nil,
		ElapsedMs:             0,
	}, nil
}

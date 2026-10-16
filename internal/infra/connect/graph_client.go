package connect

import (
	"context"
	"fmt"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	graphv1 "github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1"
	"github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1/graphv1connect"
	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type GraphSupplyClient struct {
	client graphv1connect.GraphSupplyServiceClient
}

func NewGraphSupplyClient(httpClient connecthttp.HTTPClient, baseURL string) *GraphSupplyClient {
	return &GraphSupplyClient{
		client: graphv1connect.NewGraphSupplyServiceClient(
			connectrpc.NewClient(connecthttp.NewTransport(httpClient, baseURL), forwardAuthorization()),
		),
	}
}

func (c *GraphSupplyClient) CurrentGraph(ctx context.Context, eventID string) (domain.Graph, error) {
	res, err := c.client.GetCurrentRevision(ctx, &graphv1.GetCurrentRevisionRequest{EventId: eventID})
	if err != nil {
		return domain.Graph{}, fmt.Errorf("call GetCurrentRevision: %w", err)
	}

	graph := domain.Graph{
		EventID:    res.GetEventId(),
		RevisionID: res.GetRevisionId(),
		Points:     make([]domain.Point, 0, len(res.GetPoints())),
		Routes:     make([]domain.Route, 0, len(res.GetRoutes())),
	}

	for _, point := range res.GetPoints() {
		var boundary *domain.Boundary
		if point.GetBoundary() != nil {
			boundary = &domain.Boundary{
				Direction: domain.BoundaryDirection(point.GetBoundary().GetDirection()),
				Active:    point.GetBoundary().GetActive(),
			}
		}

		graph.Points = append(graph.Points, domain.Point{
			ID:       point.GetPointId(),
			Type:     domain.PointType(point.GetType()),
			Boundary: boundary,
		})
	}

	for _, route := range res.GetRoutes() {
		graph.Routes = append(graph.Routes, domain.Route{
			ID:           route.GetRouteId(),
			FromPointID:  route.GetFromPointId(),
			ToPointID:    route.GetToPointId(),
			OneWay:       route.GetDirection() == kernelv1.DirectionAttribute_DIRECTION_ATTRIBUTE_ONE_WAY,
			CapacityHint: route.CapacityHint,
		})
	}

	return graph, nil
}

func (c *GraphSupplyClient) ObservationPointMappings(
	ctx context.Context,
	eventID, revisionID string,
) ([]domain.ObservationPointMapping, error) {
	res, err := c.client.GetObservationPointMappings(ctx, &graphv1.GetMappingsRequest{EventId: eventID, RevisionId: revisionID})
	if err != nil {
		return nil, fmt.Errorf("call GetObservationPointMappings: %w", err)
	}

	mappings := make([]domain.ObservationPointMapping, 0, len(res.GetMappings()))
	for _, mapping := range res.GetMappings() {
		mappings = append(mappings, domain.ObservationPointMapping{
			ObservationPointID: domain.ObservationPointID(mapping.GetObservationPointId()),
			Anchor: domain.GraphAnchor{
				PointID: mapping.GetAnchor().GetPointId(),
				RouteID: mapping.GetAnchor().GetRouteId(),
			},
			Method: domain.MeasurementMethodCamera,
		})
	}

	return mappings, nil
}

func (c *GraphSupplyClient) QrLocations(
	ctx context.Context,
	tenantPublicID, eventID, revisionID string,
) ([]domain.ObservationPointMapping, error) {
	res, err := c.client.GetQrLocations(ctx, &graphv1.GetQrLocationsRequest{
		EventId: eventID, RevisionId: revisionID, TenantId: tenantPublicID,
	})
	if connectrpc.CodeOf(err) == connectrpc.CodePermissionDenied {
		return nil, fmt.Errorf("call GetQrLocations: %w: %w", application.ErrForeignEvent, err)
	}

	if err != nil {
		return nil, fmt.Errorf("call GetQrLocations: %w", err)
	}

	mappings := make([]domain.ObservationPointMapping, 0, len(res.GetQrLocations()))
	for _, location := range res.GetQrLocations() {
		mappings = append(mappings, domain.ObservationPointMapping{
			ObservationPointID: domain.ObservationPointID(location.GetQrLocationId()),
			Anchor: domain.GraphAnchor{
				PointID: location.GetAnchor().GetPointId(),
				RouteID: location.GetAnchor().GetRouteId(),
			},
			Method: domain.MeasurementMethodQR,
		})
	}

	return mappings, nil
}

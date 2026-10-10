package connect

import (
	"context"
	"fmt"

	connectrpc "connectrpc.com/connect"

	graphv1 "github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1"
	"github.com/pj-hoakari/tolo-graph-authoring/gen/tolo/graph/v1/graphv1connect"
	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type GraphSupplyClient struct {
	client graphv1connect.GraphSupplyServiceClient
}

func NewGraphSupplyClient(httpClient connectrpc.HTTPClient, baseURL string) *GraphSupplyClient {
	return &GraphSupplyClient{
		client: graphv1connect.NewGraphSupplyServiceClient(
			httpClient, baseURL, connectrpc.WithInterceptors(forwardAuthorization()),
		),
	}
}

func (c *GraphSupplyClient) CurrentGraph(ctx context.Context, eventID string) (domain.Graph, error) {
	res, err := c.client.GetCurrentRevision(ctx, connectrpc.NewRequest(&graphv1.GetCurrentRevisionRequest{EventId: eventID}))
	if err != nil {
		return domain.Graph{}, fmt.Errorf("call GetCurrentRevision: %w", err)
	}

	graph := domain.Graph{
		EventID:    res.Msg.GetEventId(),
		RevisionID: res.Msg.GetRevisionId(),
		Points:     make([]domain.Point, 0, len(res.Msg.GetPoints())),
		Routes:     make([]domain.Route, 0, len(res.Msg.GetRoutes())),
	}

	for _, point := range res.Msg.GetPoints() {
		graph.Points = append(graph.Points, domain.Point{
			ID:             point.GetPointId(),
			Type:           domain.PointType(point.GetType()),
			IsBoundary:     point.GetIsBoundary(),
			BoundaryActive: point.GetBoundaryActive(),
		})
	}

	for _, route := range res.Msg.GetRoutes() {
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
	eventID string,
) ([]domain.ObservationPointMapping, error) {
	res, err := c.client.GetObservationPointMappings(ctx, connectrpc.NewRequest(&graphv1.GetMappingsRequest{EventId: eventID}))
	if err != nil {
		return nil, fmt.Errorf("call GetObservationPointMappings: %w", err)
	}

	mappings := make([]domain.ObservationPointMapping, 0, len(res.Msg.GetMappings()))
	for _, mapping := range res.Msg.GetMappings() {
		mappings = append(mappings, domain.ObservationPointMapping{
			ObservationPointID: domain.ObservationPointID(mapping.GetObservationPointId()),
			Anchor: domain.GraphAnchor{
				PointID: mapping.GetAnchor().GetPointId(),
				RouteID: mapping.GetAnchor().GetRouteId(),
			},
		})
	}

	return mappings, nil
}

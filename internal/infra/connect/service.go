package connect

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	kernelv1 "github.com/pj-hoakari/tolo-kernel-proto/gen/tolo/kernel/v1"
	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1/observationv1connect"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type MeasurementIngestService struct {
	observationv1connect.UnimplementedMeasurementIngestServiceHandler

	useCases application.MeasurementIngestUseCases
}

func NewMeasurementIngestService(useCases application.MeasurementIngestUseCases) *MeasurementIngestService {
	return &MeasurementIngestService{
		UnimplementedMeasurementIngestServiceHandler: observationv1connect.UnimplementedMeasurementIngestServiceHandler{},
		useCases: useCases,
	}
}

func (s *MeasurementIngestService) ReportMeasurements(
	ctx context.Context,
	req *observationv1.ReportMeasurementsRequest,
) (*observationv1.ReportMeasurementsResponse, error) {
	reported := req.GetMeasurements()
	measurements := make([]application.MeasurementInput, 0, len(reported))

	method := domain.MeasurementMethodCamera
	if len(reported) > 0 && reported[0].GetSource() == observationv1.MeasurementSource_MEASUREMENT_SOURCE_QR {
		method = domain.MeasurementMethodQR
	}

	if method == domain.MeasurementMethodQR && req.GetEdgeDeviceId() != "" {
		return nil, clientError(ctx, errEdgeDeviceNotAllowed)
	}

	for _, measurement := range reported {
		pointID, err := measurementPointID(measurement, method)
		if err != nil {
			return nil, clientError(ctx, err)
		}

		measurements = append(measurements, application.MeasurementInput{
			ObservationPointID: pointID,
			WindowStart:        timestampTime(measurement.GetWindowStart()),
			WindowEnd:          timestampTime(measurement.GetWindowEnd()),
			CountIn:            measurement.GetCountIn(),
			CountOut:           measurement.GetCountOut(),
			MeanDetectedPeople: measurement.MeanDetectedPeople,
		})
	}

	accepted, err := s.useCases.ReportMeasurements(ctx, application.ReportMeasurementsInput{
		TenantID:     req.GetTenantId(),
		EventID:      req.GetEventId(),
		EdgeDeviceID: req.GetEdgeDeviceId(),
		Method:       method,
		Measurements: measurements,
	})
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.ReportMeasurementsResponse{AcceptedCount: accepted}, nil
}

func measurementPointID(measurement *observationv1.Measurement, method domain.MeasurementMethod) (string, error) {
	switch measurement.GetSource() {
	case observationv1.MeasurementSource_MEASUREMENT_SOURCE_EDGE:
		if method != domain.MeasurementMethodCamera {
			return "", errMixedMeasurementSources
		}

		if measurement.GetQrLocationId() != "" {
			return "", errQRLocationNotAllowed
		}

		return measurement.GetObservationPointId(), nil
	case observationv1.MeasurementSource_MEASUREMENT_SOURCE_QR:
		if method != domain.MeasurementMethodQR {
			return "", errMixedMeasurementSources
		}

		if measurement.GetObservationPointId() != "" {
			return "", errObservationPointNotAllowed
		}

		return measurement.GetQrLocationId(), nil
	case observationv1.MeasurementSource_MEASUREMENT_SOURCE_UNSPECIFIED:
		return "", errMeasurementSourceRequired
	default:
		return "", errMeasurementSourceRequired
	}
}

func timestampTime(value *timestamppb.Timestamp) time.Time {
	if value == nil {
		return time.Time{}
	}

	return value.AsTime()
}

type EdgeDeviceService struct {
	observationv1connect.UnimplementedEdgeDeviceServiceHandler

	useCases application.EdgeDeviceUseCases
}

func NewEdgeDeviceService(useCases application.EdgeDeviceUseCases) *EdgeDeviceService {
	return &EdgeDeviceService{
		UnimplementedEdgeDeviceServiceHandler: observationv1connect.UnimplementedEdgeDeviceServiceHandler{},
		useCases:                              useCases,
	}
}

func (s *EdgeDeviceService) RegisterEdgeDevice(
	ctx context.Context,
	req *observationv1.RegisterEdgeDeviceRequest,
) (*observationv1.RegisterEdgeDeviceResponse, error) {
	output, err := s.useCases.RegisterEdgeDevice(ctx, application.RegisterEdgeDeviceInput{
		EventID:               req.GetEventId(),
		Name:                  req.GetName(),
		ObservationPointNames: req.GetObservationPointNames(),
	})
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.RegisterEdgeDeviceResponse{
		Device:              newProtoEdgeDevice(output.Device),
		ObservationPagePath: output.ObservationPagePath,
	}, nil
}

func (s *EdgeDeviceService) UnregisterEdgeDevice(
	ctx context.Context,
	req *observationv1.UnregisterEdgeDeviceRequest,
) (*observationv1.UnregisterEdgeDeviceResponse, error) {
	device, err := s.useCases.UnregisterEdgeDevice(ctx, application.UnregisterEdgeDeviceInput{
		EventID:      req.GetEventId(),
		EdgeDeviceID: req.GetEdgeDeviceId(),
	})
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.UnregisterEdgeDeviceResponse{
		Device: newProtoEdgeDevice(device),
	}, nil
}

func (s *EdgeDeviceService) ListEdgeDevices(
	ctx context.Context,
	req *observationv1.ListEdgeDevicesRequest,
) (*observationv1.ListEdgeDevicesResponse, error) {
	devices, err := s.useCases.ListEdgeDevices(ctx, application.ListEdgeDevicesInput{
		EventID:             req.GetEventId(),
		IncludeUnregistered: req.GetIncludeUnregistered(),
	})
	if err != nil {
		return nil, clientError(ctx, err)
	}

	protoDevices := make([]*observationv1.EdgeDevice, 0, len(devices))
	for _, device := range devices {
		protoDevices = append(protoDevices, newProtoEdgeDevice(device))
	}

	return &observationv1.ListEdgeDevicesResponse{Devices: protoDevices}, nil
}

func (s *EdgeDeviceService) Heartbeat(
	ctx context.Context,
	req *observationv1.HeartbeatRequest,
) (*observationv1.HeartbeatResponse, error) {
	err := s.useCases.Heartbeat(ctx, application.HeartbeatInput{
		EventID:                   req.GetEventId(),
		EdgeDeviceID:              req.GetEdgeDeviceId(),
		ActiveObservationPointIDs: req.GetActiveObservationPointIds(),
	})
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.HeartbeatResponse{}, nil
}

func (s *EdgeDeviceService) UpdateObservationPointConfig(
	ctx context.Context,
	req *observationv1.UpdateObservationPointConfigRequest,
) (*observationv1.UpdateObservationPointConfigResponse, error) {
	point, err := s.useCases.UpdateObservationPointConfig(ctx, application.UpdateObservationPointConfigInput{
		EventID:            req.GetEventId(),
		ObservationPointID: req.GetObservationPointId(),
		Name:               req.GetName(),
		Enabled:            req.GetEnabled(),
	})
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.UpdateObservationPointConfigResponse{
		Point: newProtoObservationPoint(point),
	}, nil
}

func newProtoEdgeDevice(view application.EdgeDeviceView) *observationv1.EdgeDevice {
	device := view.Device

	var lastHeartbeatAt *timestamppb.Timestamp
	if device.LastHeartbeatAt != nil {
		lastHeartbeatAt = timestamppb.New(*device.LastHeartbeatAt)
	}

	points := make([]*observationv1.ObservationPoint, 0, len(device.ObservationPoints))
	for _, point := range device.ObservationPoints {
		points = append(points, newProtoObservationPoint(point))
	}

	return &observationv1.EdgeDevice{
		EdgeDeviceId:      string(device.ID),
		EventId:           device.EventID,
		Name:              device.Name,
		ObservationPoints: points,
		Unregistered:      device.Unregistered,
		LastHeartbeatAt:   lastHeartbeatAt,
		Online:            view.Online,
	}
}

func newProtoObservationPoint(point domain.ObservationPoint) *observationv1.ObservationPoint {
	return &observationv1.ObservationPoint{
		ObservationPointId: string(point.ID),
		Name:               point.Name,
		Enabled:            point.Enabled,
		Method:             observationv1.MeasurementMethod_MEASUREMENT_METHOD_CAMERA,
	}
}

type ManualInterventionService struct {
	observationv1connect.UnimplementedManualInterventionServiceHandler
}

func NewManualInterventionService() *ManualInterventionService {
	return &ManualInterventionService{
		UnimplementedManualInterventionServiceHandler: observationv1connect.UnimplementedManualInterventionServiceHandler{},
	}
}

type StatusQueryService struct {
	observationv1connect.UnimplementedStatusQueryServiceHandler

	useCases application.StatusQueryUseCases
}

func NewStatusQueryService(useCases application.StatusQueryUseCases) *StatusQueryService {
	return &StatusQueryService{
		UnimplementedStatusQueryServiceHandler: observationv1connect.UnimplementedStatusQueryServiceHandler{},
		useCases:                               useCases,
	}
}

func (s *StatusQueryService) GetEventOverview(
	ctx context.Context,
	req *observationv1.GetEventOverviewRequest,
) (*observationv1.GetEventOverviewResponse, error) {
	overview, err := s.useCases.GetEventOverview(ctx, req.GetEventId())
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.GetEventOverviewResponse{
		Overview: &observationv1.EventOverview{
			Snapshot:    newProtoSnapshot(overview.Snapshot),
			Risks:       nil,
			DangerFlags: nil,
		},
	}, nil
}

func newProtoSnapshot(snapshot *domain.Snapshot) *kernelv1.ObservationSnapshot {
	if snapshot == nil {
		return nil
	}

	points := make([]*kernelv1.PointScore, 0, len(snapshot.PointScores)+len(snapshot.Missing.PointIDs))
	for _, score := range snapshot.PointScores {
		points = append(points, &kernelv1.PointScore{
			PointId:        score.PointID,
			PeopleScore:    score.PeopleScore,
			OccupancyDelta: proto.Float64(score.OccupancyDelta),
			Exhaustive:     score.Exhaustive,
		})
	}

	for _, pointID := range snapshot.Missing.PointIDs {
		points = append(points, &kernelv1.PointScore{PointId: pointID, PeopleScore: nil, OccupancyDelta: nil, Exhaustive: false})
	}

	routes := make([]*kernelv1.RouteScore, 0, len(snapshot.RouteScores)+len(snapshot.Missing.RouteIDs))
	for _, score := range snapshot.RouteScores {
		route := &kernelv1.RouteScore{
			RouteId: score.RouteID,
			Flow: &kernelv1.RouteScore_DirectionalFlow{
				DirectionalFlow: &kernelv1.DirectionalFlow{Forward: score.Forward, Backward: score.Backward},
			},
			StagnationScore: score.StagnationScore,
			TurnRatio:       nil,
			Exhaustive:      score.Exhaustive,
		}
		if score.ScalarFlow != nil {
			route.Flow = &kernelv1.RouteScore_ScalarFlow{ScalarFlow: *score.ScalarFlow}
		}

		routes = append(routes, route)
	}

	for _, routeID := range snapshot.Missing.RouteIDs {
		routes = append(routes, &kernelv1.RouteScore{
			RouteId: routeID, Flow: nil, StagnationScore: nil, TurnRatio: nil, Exhaustive: false,
		})
	}

	return &kernelv1.ObservationSnapshot{
		SnapshotId:  snapshot.ID,
		EventId:     snapshot.EventID,
		WindowStart: timestamppb.New(snapshot.WindowStart),
		WindowEnd:   timestamppb.New(snapshot.WindowEnd),
		PointScores: points,
		RouteScores: routes,
	}
}

type ObservationSettingsService struct {
	observationv1connect.UnimplementedObservationSettingsServiceHandler

	useCases application.ObservationSettingsUseCases
}

func NewObservationSettingsService(useCases application.ObservationSettingsUseCases) *ObservationSettingsService {
	return &ObservationSettingsService{
		UnimplementedObservationSettingsServiceHandler: observationv1connect.UnimplementedObservationSettingsServiceHandler{},
		useCases: useCases,
	}
}

func (s *ObservationSettingsService) GetObservationSettings(
	ctx context.Context,
	req *observationv1.GetObservationSettingsRequest,
) (*observationv1.GetObservationSettingsResponse, error) {
	settings, err := s.useCases.GetObservationSettings(ctx, req.GetEventId())
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.GetObservationSettingsResponse{
		Settings: newProtoObservationSettings(settings),
	}, nil
}

func (s *ObservationSettingsService) UpdateObservationSettings(
	ctx context.Context,
	req *observationv1.UpdateObservationSettingsRequest,
) (*observationv1.UpdateObservationSettingsResponse, error) {
	settings, err := s.useCases.UpdateObservationSettings(ctx, application.UpdateObservationSettingsInput{
		EventID:           req.GetEventId(),
		HistoryWindowDays: req.GetSettings().GetHistoryWindowDays(),
		EventType:         domainEventType(req.GetSettings().GetEventType()),
	})
	if err != nil {
		return nil, clientError(ctx, err)
	}

	return &observationv1.UpdateObservationSettingsResponse{
		Settings: newProtoObservationSettings(settings),
	}, nil
}

func newProtoObservationSettings(settings domain.ObservationSettings) *observationv1.ObservationSettings {
	return &observationv1.ObservationSettings{
		HistoryWindowDays: settings.HistoryWindowDays,
		EventType:         protoEventType(settings.EventType),
	}
}

func domainEventType(eventType observationv1.EventType) domain.EventType {
	switch eventType {
	case observationv1.EventType_EVENT_TYPE_SHORT_TERM:
		return domain.EventTypeShortTerm
	case observationv1.EventType_EVENT_TYPE_LONG_TERM:
		return domain.EventTypeLongTerm
	case observationv1.EventType_EVENT_TYPE_UNSPECIFIED:
		return domain.EventTypeUnspecified
	default:
		return domain.EventTypeUnspecified
	}
}

func protoEventType(eventType domain.EventType) observationv1.EventType {
	switch eventType {
	case domain.EventTypeShortTerm:
		return observationv1.EventType_EVENT_TYPE_SHORT_TERM
	case domain.EventTypeLongTerm:
		return observationv1.EventType_EVENT_TYPE_LONG_TERM
	case domain.EventTypeUnspecified:
		return observationv1.EventType_EVENT_TYPE_UNSPECIFIED
	default:
		return observationv1.EventType_EVENT_TYPE_UNSPECIFIED
	}
}

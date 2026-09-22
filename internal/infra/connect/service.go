package connect

import (
	"context"
	"errors"
	"log/slog"
	"time"

	connectrpc "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1/observationv1connect"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

// errInternal is the only detail a client learns about an internal failure.
var errInternal = errors.New("internal error")

var (
	errMeasurementSourceRequired = errors.New("measurement source is required")
	errQRLocationNotAllowed      = errors.New("qr_location_id is not allowed for edge measurements")
	errQRUnimplemented           = errors.New("QR measurements are not implemented")
)

// InternalError reports a failure the client can do nothing about. The cause is
// written to the server log and replaced by a fixed message, so that no
// internal detail leaves the service. The log handler names the trace of the
// request context on the record, so an operator can find the failure in the
// trace it belongs to.
//
// A cancelled or timed-out request is the client going away rather than a
// server fault, so it keeps its own code and is not logged.
//
// It is exported for the other transports of this process, so that every
// service answers an internal failure the same way.
func InternalError(ctx context.Context, err error) *connectrpc.Error {
	if errors.Is(err, context.Canceled) {
		return connectrpc.NewError(connectrpc.CodeCanceled, err)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return connectrpc.NewError(connectrpc.CodeDeadlineExceeded, err)
	}

	slog.ErrorContext(ctx, "internal error", "error", err)

	return connectrpc.NewError(connectrpc.CodeInternal, errInternal) //nolint:forbidigo // the one place that builds internal errors
}

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
	req *connectrpc.Request[observationv1.ReportMeasurementsRequest],
) (*connectrpc.Response[observationv1.ReportMeasurementsResponse], error) {
	reported := req.Msg.GetMeasurements()
	measurements := make([]application.MeasurementInput, 0, len(reported))

	for _, measurement := range reported {
		if err := checkEdgeMeasurement(measurement); err != nil {
			return nil, err
		}

		measurements = append(measurements, application.MeasurementInput{
			ObservationPointID: measurement.GetObservationPointId(),
			WindowStart:        timestampTime(measurement.GetWindowStart()),
			WindowEnd:          timestampTime(measurement.GetWindowEnd()),
			CountIn:            measurement.GetCountIn(),
			CountOut:           measurement.GetCountOut(),
		})
	}

	accepted, err := s.useCases.ReportMeasurements(ctx, application.ReportMeasurementsInput{
		EventID:      req.Msg.GetEventId(),
		EdgeDeviceID: req.Msg.GetEdgeDeviceId(),
		Measurements: measurements,
	})
	if err != nil {
		return nil, observationError(ctx, err)
	}

	return connectrpc.NewResponse(&observationv1.ReportMeasurementsResponse{AcceptedCount: accepted}), nil
}

func checkEdgeMeasurement(measurement *observationv1.Measurement) error {
	switch measurement.GetSource() {
	case observationv1.MeasurementSource_MEASUREMENT_SOURCE_EDGE:
		if measurement.GetQrLocationId() != "" {
			return connectrpc.NewError(connectrpc.CodeInvalidArgument, errQRLocationNotAllowed)
		}

		return nil
	case observationv1.MeasurementSource_MEASUREMENT_SOURCE_QR:
		return connectrpc.NewError(connectrpc.CodeUnimplemented, errQRUnimplemented)
	case observationv1.MeasurementSource_MEASUREMENT_SOURCE_UNSPECIFIED:
		return connectrpc.NewError(connectrpc.CodeInvalidArgument, errMeasurementSourceRequired)
	default:
		return connectrpc.NewError(connectrpc.CodeInvalidArgument, errMeasurementSourceRequired)
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
	req *connectrpc.Request[observationv1.RegisterEdgeDeviceRequest],
) (*connectrpc.Response[observationv1.RegisterEdgeDeviceResponse], error) {
	output, err := s.useCases.RegisterEdgeDevice(ctx, application.RegisterEdgeDeviceInput{
		EventID:               req.Msg.GetEventId(),
		Name:                  req.Msg.GetName(),
		ObservationPointNames: req.Msg.GetObservationPointNames(),
	})
	if err != nil {
		return nil, observationError(ctx, err)
	}

	return connectrpc.NewResponse(&observationv1.RegisterEdgeDeviceResponse{
		Device:             newProtoEdgeDevice(output.Device),
		ObservationPageUrl: output.ObservationPageURL,
	}), nil
}

func (s *EdgeDeviceService) UnregisterEdgeDevice(
	ctx context.Context,
	req *connectrpc.Request[observationv1.UnregisterEdgeDeviceRequest],
) (*connectrpc.Response[observationv1.UnregisterEdgeDeviceResponse], error) {
	device, err := s.useCases.UnregisterEdgeDevice(ctx, application.UnregisterEdgeDeviceInput{
		EventID:      req.Msg.GetEventId(),
		EdgeDeviceID: req.Msg.GetEdgeDeviceId(),
	})
	if err != nil {
		return nil, observationError(ctx, err)
	}

	return connectrpc.NewResponse(&observationv1.UnregisterEdgeDeviceResponse{
		Device: newProtoEdgeDevice(device),
	}), nil
}

func (s *EdgeDeviceService) ListEdgeDevices(
	ctx context.Context,
	req *connectrpc.Request[observationv1.ListEdgeDevicesRequest],
) (*connectrpc.Response[observationv1.ListEdgeDevicesResponse], error) {
	devices, err := s.useCases.ListEdgeDevices(ctx, application.ListEdgeDevicesInput{
		EventID:             req.Msg.GetEventId(),
		IncludeUnregistered: req.Msg.GetIncludeUnregistered(),
	})
	if err != nil {
		return nil, observationError(ctx, err)
	}

	protoDevices := make([]*observationv1.EdgeDevice, 0, len(devices))
	for _, device := range devices {
		protoDevices = append(protoDevices, newProtoEdgeDevice(device))
	}

	return connectrpc.NewResponse(&observationv1.ListEdgeDevicesResponse{Devices: protoDevices}), nil
}

func (s *EdgeDeviceService) Heartbeat(
	ctx context.Context,
	req *connectrpc.Request[observationv1.HeartbeatRequest],
) (*connectrpc.Response[observationv1.HeartbeatResponse], error) {
	err := s.useCases.Heartbeat(ctx, application.HeartbeatInput{
		EventID:                   req.Msg.GetEventId(),
		EdgeDeviceID:              req.Msg.GetEdgeDeviceId(),
		ActiveObservationPointIDs: req.Msg.GetActiveObservationPointIds(),
	})
	if err != nil {
		return nil, observationError(ctx, err)
	}

	return connectrpc.NewResponse(&observationv1.HeartbeatResponse{}), nil
}

func (s *EdgeDeviceService) UpdateObservationPointConfig(
	ctx context.Context,
	req *connectrpc.Request[observationv1.UpdateObservationPointConfigRequest],
) (*connectrpc.Response[observationv1.UpdateObservationPointConfigResponse], error) {
	point, err := s.useCases.UpdateObservationPointConfig(ctx, application.UpdateObservationPointConfigInput{
		EventID:            req.Msg.GetEventId(),
		ObservationPointID: req.Msg.GetObservationPointId(),
		Name:               req.Msg.GetName(),
		Enabled:            req.Msg.GetEnabled(),
	})
	if err != nil {
		return nil, observationError(ctx, err)
	}

	return connectrpc.NewResponse(&observationv1.UpdateObservationPointConfigResponse{
		Point: newProtoObservationPoint(point),
	}), nil
}

func newProtoEdgeDevice(device domain.EdgeDevice) *observationv1.EdgeDevice {
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

func observationError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalidPublicID),
		errors.Is(err, domain.ErrTenantRequired),
		errors.Is(err, domain.ErrEventRequired),
		errors.Is(err, domain.ErrEdgeDeviceNameRequired),
		errors.Is(err, domain.ErrObservationPointNameRequired),
		errors.Is(err, domain.ErrInvalidMeasurementWindow),
		errors.Is(err, domain.ErrNegativeCount),
		errors.Is(err, application.ErrEdgeDeviceRequired):
		return connectrpc.NewError(connectrpc.CodeInvalidArgument, err)
	case errors.Is(err, repository.ErrEdgeDeviceNotFound),
		errors.Is(err, domain.ErrObservationPointNotFound):
		return connectrpc.NewError(connectrpc.CodeNotFound, err)
	case errors.Is(err, domain.ErrEdgeDeviceUnregistered):
		return connectrpc.NewError(connectrpc.CodeFailedPrecondition, err)
	case errors.Is(err, tenantctx.ErrMissing),
		errors.Is(err, tenantctx.ErrMismatch),
		errors.Is(err, tenantctx.ErrEventMissing),
		errors.Is(err, tenantctx.ErrEventMismatch),
		errors.Is(err, application.ErrForeignEvent):
		return connectrpc.NewError(connectrpc.CodePermissionDenied, err)
	default:
		return InternalError(ctx, err)
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
}

func NewStatusQueryService() *StatusQueryService {
	return &StatusQueryService{
		UnimplementedStatusQueryServiceHandler: observationv1connect.UnimplementedStatusQueryServiceHandler{},
	}
}

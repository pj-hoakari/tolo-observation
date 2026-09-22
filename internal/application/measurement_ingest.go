package application

//go:generate go tool mockgen -source=../repository/measurement.go -destination=mock_measurement_repository_test.go -package=application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

var ErrEdgeDeviceRequired = errors.New("edge_device_id is required for edge measurements")

type MeasurementInput struct {
	ObservationPointID string
	WindowStart        time.Time
	WindowEnd          time.Time
	CountIn            int32
	CountOut           int32
}

type ReportMeasurementsInput struct {
	EventID      string
	EdgeDeviceID string
	Measurements []MeasurementInput
}

type MeasurementIngestUseCases interface {
	ReportMeasurements(ctx context.Context, input ReportMeasurementsInput) (int32, error)
}

type MeasurementIngestService struct {
	devices      repository.EdgeDeviceRepository
	measurements repository.MeasurementRepository
}

func NewMeasurementIngestService(
	devices repository.EdgeDeviceRepository,
	measurements repository.MeasurementRepository,
) *MeasurementIngestService {
	return &MeasurementIngestService{devices: devices, measurements: measurements}
}

func (s *MeasurementIngestService) ReportMeasurements(
	ctx context.Context,
	input ReportMeasurementsInput,
) (int32, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return 0, err
	}

	measurements := make([]domain.Measurement, 0, len(input.Measurements))

	var accepted int32

	for _, reported := range input.Measurements {
		measurement, err := domain.NewMeasurement(
			reported.ObservationPointID,
			reported.WindowStart,
			reported.WindowEnd,
			reported.CountIn,
			reported.CountOut,
		)
		if err != nil {
			return 0, err
		}

		measurements = append(measurements, measurement)
		accepted++
	}

	tenantPublicID, err := s.resolveTenant(ctx, input, measurements)
	if err != nil {
		return 0, err
	}

	if err := s.measurements.RecordAll(ctx, tenantPublicID, input.EventID, measurements); err != nil {
		return 0, fmt.Errorf("record measurements: %w", err)
	}

	return accepted, nil
}

func (s *MeasurementIngestService) resolveTenant(
	ctx context.Context,
	input ReportMeasurementsInput,
	measurements []domain.Measurement,
) (string, error) {
	edgeDeviceID := strings.TrimSpace(input.EdgeDeviceID)
	if edgeDeviceID == "" {
		return "", ErrEdgeDeviceRequired
	}

	id, err := domain.ParseEdgeDeviceID(edgeDeviceID)
	if err != nil {
		return "", err
	}

	device, err := s.devices.FindByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("find edge device: %w", err)
	}

	if device.EventID != input.EventID {
		return "", ErrForeignEvent
	}

	if device.Unregistered {
		return "", domain.ErrEdgeDeviceUnregistered
	}

	for _, measurement := range measurements {
		if !device.HasObservationPoint(measurement.ObservationPointID) {
			return "", ErrForeignEvent
		}
	}

	return device.TenantPublicID, nil
}

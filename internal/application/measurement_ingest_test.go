package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

const (
	testEdgeDeviceID      = "1122334455667788"
	testPointID           = "99aabbccddeeff00"
	foreignPointID        = "00ffeeddccbbaa99"
	otherTenantPublicID   = "bbbbccccddddeeff"
	testMeasurementWindow = 2 * time.Minute
)

type cycleFunc func(ctx context.Context, tenantPublicID, eventID string) error

func (f cycleFunc) Run(ctx context.Context, tenantPublicID, eventID string) error {
	return f(ctx, tenantPublicID, eventID)
}

func newIngestService(t *testing.T) (
	*application.MeasurementIngestService,
	*MockEdgeDeviceRepository,
	*MockMeasurementRepository,
) {
	t.Helper()

	service, devices, measurements := newIngestServiceWithCycle(t, cycleFunc(func(context.Context, string, string) error {
		t.Error("observation cycle ran for a rejected report")

		return nil
	}))

	return service, devices, measurements
}

func newIngestServiceWithCycle(t *testing.T, cycle application.ObservationCycleRunner) (
	*application.MeasurementIngestService,
	*MockEdgeDeviceRepository,
	*MockMeasurementRepository,
) {
	t.Helper()

	controller := gomock.NewController(t)
	devices := NewMockEdgeDeviceRepository(controller)
	measurements := NewMockMeasurementRepository(controller)

	return application.NewMeasurementIngestService(devices, measurements, cycle), devices, measurements
}

func edgeMeasurement(pointID string) application.MeasurementInput {
	return application.MeasurementInput{
		ObservationPointID: pointID,
		WindowStart:        testNow.Add(-testMeasurementWindow),
		WindowEnd:          testNow,
		CountIn:            4,
		CountOut:           2,
	}
}

func registeredDevice() domain.EdgeDevice {
	return domain.EdgeDevice{
		ID:             testEdgeDeviceID,
		TenantPublicID: otherTenantPublicID,
		EventID:        testEventPublicID,
		Name:           "gate-1",
		ObservationPoints: []domain.ObservationPoint{
			{ID: testPointID, Name: "north", Enabled: true},
		},
	}
}

func TestReportMeasurementsRecordsUnderTheDevicesTenant(t *testing.T) {
	t.Parallel()

	var cycled []string

	service, devices, measurements := newIngestServiceWithCycle(t, cycleFunc(
		func(_ context.Context, tenantPublicID, eventID string) error {
			cycled = append(cycled, tenantPublicID, eventID)

			return errors.New("flow control unreachable")
		},
	))
	devices.EXPECT().
		FindByID(gomock.Any(), domain.EdgeDeviceID(testEdgeDeviceID)).
		Return(registeredDevice(), nil)
	measurements.EXPECT().
		RecordAll(gomock.Any(), otherTenantPublicID, testEventPublicID, gomock.Len(2)).
		Return(nil)

	accepted, err := service.ReportMeasurements(
		withEventToken(testEventPublicID),
		application.ReportMeasurementsInput{
			EventID:      testEventPublicID,
			EdgeDeviceID: testEdgeDeviceID,
			Measurements: []application.MeasurementInput{
				edgeMeasurement(testPointID),
				edgeMeasurement(testPointID),
			},
		},
	)
	if err != nil {
		t.Fatalf("ReportMeasurements() error = %v", err)
	}

	if got, want := accepted, int32(2); got != want {
		t.Errorf("accepted = %d, want %d", got, want)
	}

	if want := []string{otherTenantPublicID, testEventPublicID}; !slices.Equal(cycled, want) {
		t.Errorf("observation cycle ran with %v, want %v", cycled, want)
	}
}

func TestReportMeasurementsRejectsAnotherDevicesObservationPoint(t *testing.T) {
	t.Parallel()

	service, devices, _ := newIngestService(t)
	devices.EXPECT().
		FindByID(gomock.Any(), domain.EdgeDeviceID(testEdgeDeviceID)).
		Return(registeredDevice(), nil)

	_, err := service.ReportMeasurements(
		withEventToken(testEventPublicID),
		application.ReportMeasurementsInput{
			EventID:      testEventPublicID,
			EdgeDeviceID: testEdgeDeviceID,
			Measurements: []application.MeasurementInput{edgeMeasurement(foreignPointID)},
		},
	)
	if !errors.Is(err, application.ErrForeignEvent) {
		t.Fatalf("ReportMeasurements() error = %v, want %v", err, application.ErrForeignEvent)
	}
}

func TestReportMeasurementsRejectsUnregisteredDevice(t *testing.T) {
	t.Parallel()

	service, devices, _ := newIngestService(t)

	device := registeredDevice()
	device.Unregistered = true

	devices.EXPECT().
		FindByID(gomock.Any(), domain.EdgeDeviceID(testEdgeDeviceID)).
		Return(device, nil)

	_, err := service.ReportMeasurements(
		withEventToken(testEventPublicID),
		application.ReportMeasurementsInput{
			EventID:      testEventPublicID,
			EdgeDeviceID: testEdgeDeviceID,
			Measurements: []application.MeasurementInput{edgeMeasurement(testPointID)},
		},
	)
	if !errors.Is(err, domain.ErrEdgeDeviceUnregistered) {
		t.Fatalf("ReportMeasurements() error = %v, want %v", err, domain.ErrEdgeDeviceUnregistered)
	}
}

func TestReportMeasurementsRequiresAnEdgeDevice(t *testing.T) {
	t.Parallel()

	service, _, _ := newIngestService(t)

	_, err := service.ReportMeasurements(
		withEventToken(testEventPublicID),
		application.ReportMeasurementsInput{
			EventID:      testEventPublicID,
			EdgeDeviceID: "",
			Measurements: []application.MeasurementInput{edgeMeasurement(testPointID)},
		},
	)
	if !errors.Is(err, application.ErrEdgeDeviceRequired) {
		t.Fatalf("ReportMeasurements() error = %v, want %v", err, application.ErrEdgeDeviceRequired)
	}
}

func TestReportMeasurementsRejectsTheWholeBatchOnAnInvalidWindow(t *testing.T) {
	t.Parallel()

	service, _, _ := newIngestService(t)

	invalid := edgeMeasurement(testPointID)
	invalid.WindowEnd = invalid.WindowStart

	_, err := service.ReportMeasurements(
		withEventToken(testEventPublicID),
		application.ReportMeasurementsInput{
			EventID:      testEventPublicID,
			EdgeDeviceID: testEdgeDeviceID,
			Measurements: []application.MeasurementInput{edgeMeasurement(testPointID), invalid},
		},
	)
	if !errors.Is(err, domain.ErrInvalidMeasurementWindow) {
		t.Fatalf("ReportMeasurements() error = %v, want %v", err, domain.ErrInvalidMeasurementWindow)
	}
}

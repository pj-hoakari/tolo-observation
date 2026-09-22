package connect

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/types/known/timestamppb"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1/observationv1connect"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	dbinfra "github.com/pj-hoakari/tolo-observation/internal/infra/db"
)

const otherEventPublicID = "9876543210fedcba"

var testDB *sqlx.DB

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("tolo_observation"),
		postgres.WithUsername("tolo_observation"),
		postgres.WithPassword("tolo_observation"),
		postgres.WithInitScripts(migrationPaths()...),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start PostgreSQL test container: %v\n", err)
		os.Exit(1)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err == nil {
		testDB, err = dbinfra.Open(ctx, databaseURL)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to PostgreSQL test container: %v\n", err)

		_ = container.Terminate(context.Background())

		os.Exit(1)
	}

	code := m.Run()
	_ = testDB.Close()
	_ = container.Terminate(context.Background())

	os.Exit(code)
}

func migrationPaths() []string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("locate test source file")
	}

	pattern := filepath.Join(filepath.Dir(filename), "..", "..", "..", "migrations", "*.up.sql")

	paths, err := filepath.Glob(pattern)
	if err != nil {
		panic(fmt.Sprintf("glob migration files %s: %v", pattern, err))
	}

	if len(paths) == 0 {
		panic(fmt.Sprintf("no migration files match %s", pattern))
	}

	slices.Sort(paths)

	return paths
}

type observationFlow struct {
	generator     *jwtgen.Generator
	edgeDevices   observationv1connect.EdgeDeviceServiceClient
	measurements  observationv1connect.MeasurementIngestServiceClient
	observedStart time.Time
}

func newObservationFlow(t *testing.T) observationFlow {
	t.Helper()

	generator, err := jwtgen.NewGenerator("observation-flow-key")
	if err != nil {
		t.Fatalf("create token generator: %v", err)
	}

	keys, err := generator.JWKS()
	if err != nil {
		t.Fatalf("publish signing keys: %v", err)
	}

	devices := dbinfra.NewPostgresEdgeDeviceRepository(testDB)

	routes, err := RoutesWithVerifier(
		newTestVerifier(t, keys),
		application.NewEdgeDeviceService(devices, application.EdgeDeviceConfig{
			ObservationPageBaseURL: "https://example.test/observe",
			HeartbeatTimeout:       time.Hour,
			Now:                    time.Now,
		}),
		application.NewMeasurementIngestService(devices, dbinfra.NewPostgresMeasurementRepository(testDB)),
	)
	if err != nil {
		t.Fatalf("RoutesWithVerifier() error = %v", err)
	}

	mux := http.NewServeMux()
	routes(mux)

	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	return observationFlow{
		generator:     generator,
		edgeDevices:   observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL),
		measurements:  observationv1connect.NewMeasurementIngestServiceClient(httpServer.Client(), httpServer.URL),
		observedStart: time.Now().Add(-time.Minute),
	}
}

func (f observationFlow) token(t *testing.T, eventPublicID, scope string) string {
	t.Helper()

	output, err := f.generator.Generate(jwtgen.Config{
		Issuer:         DefaultInternalJWTIssuer,
		Audience:       DefaultInternalJWTAudience,
		TokenUse:       internaljwt.TokenUseEventAccess,
		TenantPublicID: testTenantPublicID,
		EventPublicID:  eventPublicID,
		Scope:          scope,
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("mint %s token: %v", scope, err)
	}

	return "Bearer " + output.Token
}

func authorized[T any](authorization string, message *T) *connectrpc.Request[T] {
	req := connectrpc.NewRequest(message)
	req.Header().Set("Authorization", authorization)

	return req
}

func TestObservationFlow(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(ctx, authorized(
		flow.token(t, testEventPublicID, "events.manage"),
		&observationv1.RegisterEdgeDeviceRequest{
			EventId:               testEventPublicID,
			Name:                  "gate-1",
			ObservationPointNames: []string{"north", "south"},
		},
	))
	if err != nil {
		t.Fatalf("RegisterEdgeDevice() error = %v", err)
	}

	deviceID := registered.Msg.GetDevice().GetEdgeDeviceId()

	pointIDs := make([]string, 0, 2)
	for _, point := range registered.Msg.GetDevice().GetObservationPoints() {
		pointIDs = append(pointIDs, point.GetObservationPointId())
	}

	if len(pointIDs) != 2 {
		t.Fatalf("observation_points = %v, want two points", pointIDs)
	}

	reportToken := flow.token(t, testEventPublicID, "events.report")

	if _, err = flow.edgeDevices.Heartbeat(ctx, authorized(reportToken, &observationv1.HeartbeatRequest{
		EventId:                   testEventPublicID,
		EdgeDeviceId:              deviceID,
		ActiveObservationPointIds: pointIDs,
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	reported, err := flow.measurements.ReportMeasurements(ctx, authorized(
		reportToken,
		&observationv1.ReportMeasurementsRequest{
			EventId:      testEventPublicID,
			EdgeDeviceId: deviceID,
			Measurements: []*observationv1.Measurement{
				edgeMeasurement(pointIDs[0], flow.observedStart),
				edgeMeasurement(pointIDs[1], flow.observedStart),
			},
		},
	))
	if err != nil {
		t.Fatalf("ReportMeasurements() error = %v", err)
	}

	if got, want := reported.Msg.GetAcceptedCount(), int32(2); got != want {
		t.Errorf("accepted_count = %d, want %d", got, want)
	}

	listed, err := flow.edgeDevices.ListEdgeDevices(ctx, authorized(
		flow.token(t, testEventPublicID, "events.read"),
		&observationv1.ListEdgeDevicesRequest{EventId: testEventPublicID, IncludeUnregistered: false},
	))
	if err != nil {
		t.Fatalf("ListEdgeDevices() error = %v", err)
	}

	devices := listed.Msg.GetDevices()
	if len(devices) != 1 {
		t.Fatalf("devices = %v, want one device", devices)
	}

	for _, point := range devices[0].GetObservationPoints() {
		if !point.GetEnabled() {
			t.Errorf("point %s enabled = false, want true", point.GetObservationPointId())
		}
	}

	t.Run("refuses a token minted for another event", func(t *testing.T) {
		_, err := flow.edgeDevices.Heartbeat(ctx, authorized(
			flow.token(t, otherEventPublicID, "events.report"),
			&observationv1.HeartbeatRequest{
				EventId:                   testEventPublicID,
				EdgeDeviceId:              deviceID,
				ActiveObservationPointIds: nil,
			},
		))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Fatalf("Heartbeat() error code = %v, want %v", got, want)
		}
	})

	t.Run("refuses a request naming another event", func(t *testing.T) {
		_, err := flow.edgeDevices.UnregisterEdgeDevice(ctx, authorized(
			flow.token(t, testEventPublicID, "events.manage"),
			&observationv1.UnregisterEdgeDeviceRequest{
				EventId:      otherEventPublicID,
				EdgeDeviceId: deviceID,
			},
		))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Fatalf("UnregisterEdgeDevice() error code = %v, want %v", got, want)
		}
	})

	t.Run("refuses a window that does not advance", func(t *testing.T) {
		measurement := edgeMeasurement(pointIDs[0], flow.observedStart)
		measurement.WindowEnd = measurement.GetWindowStart()

		_, err := flow.measurements.ReportMeasurements(ctx, authorized(
			reportToken,
			&observationv1.ReportMeasurementsRequest{
				EventId:      testEventPublicID,
				EdgeDeviceId: deviceID,
				Measurements: []*observationv1.Measurement{measurement},
			},
		))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("ReportMeasurements() error code = %v, want %v", got, want)
		}
	})

	t.Run("refuses a QR measurement as unimplemented", func(t *testing.T) {
		qr := edgeMeasurement(pointIDs[0], flow.observedStart)
		qr.Source = observationv1.MeasurementSource_MEASUREMENT_SOURCE_QR
		qr.ObservationPointId = ""
		qr.QrLocationId = "qr-location-1"

		_, err := flow.measurements.ReportMeasurements(ctx, authorized(
			reportToken,
			&observationv1.ReportMeasurementsRequest{
				EventId:      testEventPublicID,
				EdgeDeviceId: deviceID,
				Measurements: []*observationv1.Measurement{
					edgeMeasurement(pointIDs[0], flow.observedStart),
					qr,
				},
			},
		))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnimplemented; got != want {
			t.Fatalf("ReportMeasurements() error code = %v, want %v", got, want)
		}
	})

	t.Run("refuses measurements from an unregistered device", func(t *testing.T) {
		if _, err := flow.edgeDevices.UnregisterEdgeDevice(ctx, authorized(
			flow.token(t, testEventPublicID, "events.manage"),
			&observationv1.UnregisterEdgeDeviceRequest{EventId: testEventPublicID, EdgeDeviceId: deviceID},
		)); err != nil {
			t.Fatalf("UnregisterEdgeDevice() error = %v", err)
		}

		_, err := flow.measurements.ReportMeasurements(ctx, authorized(
			reportToken,
			&observationv1.ReportMeasurementsRequest{
				EventId:      testEventPublicID,
				EdgeDeviceId: deviceID,
				Measurements: []*observationv1.Measurement{edgeMeasurement(pointIDs[0], flow.observedStart)},
			},
		))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Fatalf("ReportMeasurements() error code = %v, want %v", got, want)
		}
	})
}

func edgeMeasurement(pointID string, windowStart time.Time) *observationv1.Measurement {
	return &observationv1.Measurement{
		Source:             observationv1.MeasurementSource_MEASUREMENT_SOURCE_EDGE,
		ObservationPointId: pointID,
		WindowStart:        timestamppb.New(windowStart),
		WindowEnd:          timestamppb.New(windowStart.Add(time.Minute)),
		CountIn:            7,
		CountOut:           3,
	}
}

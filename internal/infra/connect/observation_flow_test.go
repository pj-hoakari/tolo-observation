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

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1/observationv1connect"
	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
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
	statusQueries observationv1connect.StatusQueryServiceClient
	settings      observationv1connect.ObservationSettingsServiceClient
	graph         *graphStub
	flow          *flowStub
	operation     *operationStub
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
	holds := application.NewHeartbeatLossHolds(dbinfra.NewPostgresHeartbeatLossHoldRepository(testDB), domain.HeartbeatLossHoldPolicy{
		HeartbeatTimeout: time.Hour,
		Threshold:        0.8,
		MinDevices:       10,
		MaxDuration:      5 * time.Minute,
	})
	measurements := dbinfra.NewPostgresMeasurementRepository(testDB)
	snapshots := dbinfra.NewPostgresSnapshotRepository(testDB)
	settings := dbinfra.NewPostgresObservationSettingsRepository(testDB)
	graph, graphURL := startGraphStub(t)
	flow, flowURL := startFlowStub(t)
	operation, operationURL := startOperationStub(t)

	graphs := NewGraphSupplyClient(http.DefaultClient, graphURL)

	cycle := application.NewObservationCycle(
		graphs,
		NewFlowControlClient(flowURL),
		NewOperationClient(http.DefaultClient, operationURL),
		devices,
		holds,
		measurements,
		snapshots,
		settings,
		application.ObservationCycleConfig{
			Window:           time.Minute,
			OptimizeTimeout:  5 * time.Second,
			OperationTimeout: operationTestTimeout,
			Now:              func() time.Time { return time.Now().Add(123 * time.Nanosecond) },
		},
	)

	routes, err := RoutesWithVerifier(
		newTestVerifier(t, keys),
		application.NewEdgeDeviceService(devices, holds, application.EdgeDeviceConfig{
			HeartbeatTimeout: time.Hour,
			Now:              time.Now,
		}),
		application.NewMeasurementIngestService(devices, measurements, graphs, cycle),
		application.NewStatusQueryService(snapshots, dbinfra.NewPostgresGuestStatusRepository(testDB)),
		application.NewObservationSettingsService(settings),
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
		edgeDevices:   observationv1connect.NewEdgeDeviceServiceClient(connectrpc.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))),
		measurements:  observationv1connect.NewMeasurementIngestServiceClient(connectrpc.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))),
		statusQueries: observationv1connect.NewStatusQueryServiceClient(connectrpc.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))),
		settings:      observationv1connect.NewObservationSettingsServiceClient(connectrpc.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))),
		graph:         graph,
		flow:          flow,
		operation:     operation,
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

func (f observationFlow) serviceToken(t *testing.T, originSub, tenantPublicID string) string {
	t.Helper()

	output, err := f.generator.Generate(jwtgen.Config{
		Issuer:         DefaultInternalJWTIssuer,
		Audience:       DefaultInternalJWTAudience,
		TokenUse:       internaljwt.TokenUseService,
		OriginSub:      originSub,
		TenantPublicID: tenantPublicID,
		Scope:          "events.report",
		TTL:            time.Hour,
	})
	if err != nil {
		t.Fatalf("mint service token: %v", err)
	}

	return "Bearer " + output.Token
}

func authorized[T any](ctx context.Context, authorization string, message *T) (context.Context, *T) {
	ctx, info := connectrpc.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", authorization)

	return ctx, message
}

func TestObservationFlow(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()

	registered, err := flow.edgeDevices.RegisterEdgeDevice(authorized(ctx,
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

	deviceID := registered.GetDevice().GetEdgeDeviceId()

	if got, want := registered.GetObservationPagePath(), "/event/"+testEventPublicID+"/observation/"+deviceID; got != want {
		t.Errorf("observation_page_path = %q, want %q", got, want)
	}

	if registered.GetDevice().GetOnline() || registered.GetDevice().GetLastHeartbeatAt() != nil {
		t.Errorf("registered device = %v, want offline without last_heartbeat_at", registered.GetDevice())
	}

	pointIDs := make([]string, 0, 2)
	for _, point := range registered.GetDevice().GetObservationPoints() {
		pointIDs = append(pointIDs, point.GetObservationPointId())
	}

	if len(pointIDs) != 2 {
		t.Fatalf("observation_points = %v, want two points", pointIDs)
	}

	reportToken := flow.token(t, testEventPublicID, "events.report")

	if _, err = flow.edgeDevices.Heartbeat(authorized(ctx, reportToken, &observationv1.HeartbeatRequest{
		EventId:                   testEventPublicID,
		EdgeDeviceId:              deviceID,
		ActiveObservationPointIds: pointIDs,
	})); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}

	reported, err := flow.measurements.ReportMeasurements(authorized(ctx,
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

	if got, want := reported.GetAcceptedCount(), int32(2); got != want {
		t.Errorf("accepted_count = %d, want %d", got, want)
	}

	listed, err := flow.edgeDevices.ListEdgeDevices(authorized(ctx,
		flow.token(t, testEventPublicID, "events.read"),
		&observationv1.ListEdgeDevicesRequest{EventId: testEventPublicID, IncludeUnregistered: false},
	))
	if err != nil {
		t.Fatalf("ListEdgeDevices() error = %v", err)
	}

	devices := listed.GetDevices()
	if len(devices) != 1 {
		t.Fatalf("devices = %v, want one device", devices)
	}

	if !devices[0].GetOnline() {
		t.Errorf("online = false, want true after a heartbeat")
	}

	if devices[0].GetLastHeartbeatAt() == nil {
		t.Errorf("last_heartbeat_at is unset, want the heartbeat time")
	}

	for _, point := range devices[0].GetObservationPoints() {
		if !point.GetEnabled() {
			t.Errorf("point %s enabled = false, want true", point.GetObservationPointId())
		}
	}

	t.Run("refuses a token minted for another event", func(t *testing.T) {
		_, err := flow.edgeDevices.Heartbeat(authorized(ctx,
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
		_, err := flow.edgeDevices.UnregisterEdgeDevice(authorized(ctx,
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

		_, err := flow.measurements.ReportMeasurements(authorized(ctx,
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

	t.Run("refuses a QR measurement mixed into edge measurements", func(t *testing.T) {
		qr := edgeMeasurement(pointIDs[0], flow.observedStart)
		qr.Source = observationv1.MeasurementSource_MEASUREMENT_SOURCE_QR
		qr.ObservationPointId = ""
		qr.QrLocationId = "qr-location-1"

		_, err := flow.measurements.ReportMeasurements(authorized(ctx,
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
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("ReportMeasurements() error code = %v, want %v", got, want)
		}
	})

	t.Run("refuses measurements from an unregistered device", func(t *testing.T) {
		unregistered, err := flow.edgeDevices.UnregisterEdgeDevice(authorized(ctx,
			flow.token(t, testEventPublicID, "events.manage"),
			&observationv1.UnregisterEdgeDeviceRequest{EventId: testEventPublicID, EdgeDeviceId: deviceID},
		))
		if err != nil {
			t.Fatalf("UnregisterEdgeDevice() error = %v", err)
		}

		if unregistered.GetDevice().GetOnline() {
			t.Errorf("online = true, want false for an unregistered device")
		}

		_, err = flow.measurements.ReportMeasurements(authorized(ctx,
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
		MeanDetectedPeople: proto.Float64(2.5),
	}
}

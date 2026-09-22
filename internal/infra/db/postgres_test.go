package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

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
		// Open is the same instrumented entry point the server uses, so the
		// repository tests also cover the OpenTelemetry driver wrapper.
		testDB, err = Open(ctx, databaseURL)
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

func TestPostgresGreetingRepositoryRecord(t *testing.T) {
	repository := newTestRepository(t)
	ctx := internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{TenantPublicID: "a1b2c3d4e5f60718"})

	greeting, err := domain.NewGreeting("Ada")
	if err != nil {
		t.Fatalf("NewGreeting() error = %v", err)
	}

	if err := repository.Record(ctx, greeting); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	var rows []greetingRow
	if err := testDB.SelectContext(ctx, &rows, `SELECT tenant_public_id, name FROM greetings ORDER BY id`); err != nil {
		t.Fatalf("select recorded greetings: %v", err)
	}

	if want := []greetingRow{{TenantPublicID: "a1b2c3d4e5f60718", Name: "Ada"}}; !slices.Equal(rows, want) {
		t.Errorf("recorded greetings = %#v, want %#v", rows, want)
	}
}

func TestPostgresGreetingRepositoryRecordRequiresTenant(t *testing.T) {
	repository := newTestRepository(t)
	ctx := context.Background()

	greeting, err := domain.NewGreeting("Ada")
	if err != nil {
		t.Fatalf("NewGreeting() error = %v", err)
	}

	// The context carries no authenticated tenant, so recording must fail
	// closed instead of persisting an ownerless greeting.
	if err := repository.Record(ctx, greeting); !errors.Is(err, tenantctx.ErrMissing) {
		t.Fatalf("Record() error = %v, want %v", err, tenantctx.ErrMissing)
	}

	var count int
	if err := testDB.GetContext(ctx, &count, `SELECT COUNT(*) FROM greetings`); err != nil {
		t.Fatalf("count greetings: %v", err)
	}

	if count != 0 {
		t.Errorf("greetings count = %d, want 0", count)
	}
}

// TestPostgresGreetingRepositoryRecordJoinsTransaction proves the repository
// runs its statement through Executor: the write is rolled back with the
// surrounding transaction and only lands once that transaction commits.
func TestPostgresGreetingRepositoryRecordJoinsTransaction(t *testing.T) {
	repository := newTestRepository(t)
	ctx := internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{TenantPublicID: "a1b2c3d4e5f60718"})

	greeting, err := domain.NewGreeting("Ada")
	if err != nil {
		t.Fatalf("NewGreeting() error = %v", err)
	}

	errAbort := errors.New("abort")

	// The callback must return rather than call t.Fatal: a Goexit would skip
	// the rollback and leave the transaction holding its lock on greetings.
	err = RunInTransaction(ctx, testDB, func(ctx context.Context) error {
		if err := repository.Record(ctx, greeting); err != nil {
			return fmt.Errorf("record greeting: %w", err)
		}

		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("RunInTransaction() error = %v, want %v", err, errAbort)
	}

	if count := countGreetings(ctx, t); count != 0 {
		t.Errorf("greetings count after rollback = %d, want 0", count)
	}

	if err := RunInTransaction(ctx, testDB, func(ctx context.Context) error {
		return repository.Record(ctx, greeting)
	}); err != nil {
		t.Fatalf("RunInTransaction() error = %v", err)
	}

	if count := countGreetings(ctx, t); count != 1 {
		t.Errorf("greetings count after commit = %d, want 1", count)
	}
}

func TestPostgresGreetingRepositoryRecordNormalizesQueryText(t *testing.T) {
	// otel.SetTracerProvider mutates global state, so this test must not run
	// in parallel. otelsql keeps the delegating global provider it saw in
	// Open, which forwards to whatever is installed here.
	recorder := tracetest.NewSpanRecorder()
	previousProvider := otel.GetTracerProvider()

	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })

	repository := newTestRepository(t)
	ctx := internaljwt.ContextWithClaims(context.Background(), internaljwt.Claims{TenantPublicID: "a1b2c3d4e5f60718"})

	greeting, err := domain.NewGreeting("Ada")
	if err != nil {
		t.Fatalf("NewGreeting() error = %v", err)
	}

	if err := repository.Record(ctx, greeting); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	// The repository writes the statement as a multi-line raw string literal,
	// so this exact value also proves the newlines and tabs are gone.
	const wantQueryText = "INSERT INTO greetings (tenant_public_id, name) VALUES ($1, $2)"

	var (
		insertSpan sdktrace.ReadOnlySpan
		spanNames  []string
	)

	for _, span := range recorder.Ended() {
		spanNames = append(spanNames, span.Name())

		if queryText, ok := spanAttribute(span, semconv.DBQueryTextKey); ok && queryText == wantQueryText {
			insertSpan = span
		}
	}

	if insertSpan == nil {
		t.Fatalf("span with %s = %q not found, recorded spans = %v", semconv.DBQueryTextKey, wantQueryText, spanNames)
	}

	system, ok := spanAttribute(insertSpan, semconv.DBSystemNameKey)
	if !ok {
		t.Fatalf("%s on span %q = missing, want %q", semconv.DBSystemNameKey, insertSpan.Name(), "postgresql")
	}

	if system != "postgresql" {
		t.Fatalf("%s on span %q = %q, want %q", semconv.DBSystemNameKey, insertSpan.Name(), system, "postgresql")
	}
}

type greetingRow struct {
	TenantPublicID string `db:"tenant_public_id"`
	Name           string `db:"name"`
}

func newTestRepository(t *testing.T) *PostgresGreetingRepository {
	t.Helper()

	if _, err := testDB.Exec(`TRUNCATE greetings`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}

	return NewPostgresGreetingRepository(testDB)
}

func countGreetings(ctx context.Context, t *testing.T) int {
	t.Helper()

	var count int
	if err := testDB.GetContext(ctx, &count, `SELECT COUNT(*) FROM greetings`); err != nil {
		t.Fatalf("count greetings: %v", err)
	}

	return count
}

func spanAttribute(span sdktrace.ReadOnlySpan, key attribute.Key) (string, bool) {
	for _, attr := range span.Attributes() {
		if attr.Key == key {
			return attr.Value.AsString(), true
		}
	}

	return "", false
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

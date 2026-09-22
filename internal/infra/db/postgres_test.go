package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
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

package db

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type measurementRow struct {
	TenantPublicID     string    `db:"tenant_public_id"`
	EventID            string    `db:"event_id"`
	ObservationPointID string    `db:"observation_point_id"`
	WindowStart        time.Time `db:"window_start"`
	WindowEnd          time.Time `db:"window_end"`
	CountIn            int32     `db:"count_in"`
	CountOut           int32     `db:"count_out"`
	RateIn             float64   `db:"rate_in"`
	RateOut            float64   `db:"rate_out"`
	ReceivedAt         time.Time `db:"received_at"`
}

func selectMeasurements(t *testing.T, eventID string) []measurementRow {
	t.Helper()

	var rows []measurementRow

	err := sqlx.SelectContext(context.Background(), testDB, &rows, `
		SELECT tenant_public_id, event_id, observation_point_id,
			window_start, window_end, count_in, count_out, rate_in, rate_out, received_at
		FROM measurements
		WHERE event_id = $1
		ORDER BY id`, eventID)
	if err != nil {
		t.Fatalf("select measurements: %v", err)
	}

	return rows
}

func TestPostgresMeasurementRepositoryRecordAll(t *testing.T) {
	repo := NewPostgresMeasurementRepository(testDB)
	ctx := context.Background()
	device := newDevice(t, "tenant-measure", "event-measure", "in", "out")
	start := time.Now().UTC().Truncate(time.Microsecond)

	first, err := domain.NewMeasurement(string(device.ObservationPoints[0].ID),
		start, start.Add(30*time.Second), 6, 3)
	if err != nil {
		t.Fatalf("NewMeasurement first: %v", err)
	}

	second, err := domain.NewMeasurement(string(device.ObservationPoints[1].ID),
		start, start.Add(2*time.Minute), 10, 0)
	if err != nil {
		t.Fatalf("NewMeasurement second: %v", err)
	}

	if err := repo.RecordAll(ctx, "tenant-measure", "event-measure", []domain.Measurement{first, second}); err != nil {
		t.Fatalf("RecordAll: %v", err)
	}

	rows := selectMeasurements(t, "event-measure")
	if len(rows) != 2 {
		t.Fatalf("got %d measurement rows, want 2", len(rows))
	}

	firstRow := rows[0]
	if firstRow.ObservationPointID != string(device.ObservationPoints[0].ID) {
		t.Errorf("first row observation point = %q, want %q", firstRow.ObservationPointID, device.ObservationPoints[0].ID)
	}

	if firstRow.TenantPublicID != "tenant-measure" || firstRow.CountIn != 6 || firstRow.CountOut != 3 {
		t.Errorf("first row = %+v", firstRow)
	}

	if firstRow.RateIn != 12 || firstRow.RateOut != 6 {
		t.Errorf("first row rates = %v/%v, want 12/6", firstRow.RateIn, firstRow.RateOut)
	}

	if !firstRow.WindowStart.Equal(start) || !firstRow.WindowEnd.Equal(start.Add(30*time.Second)) {
		t.Errorf("first row window = %v..%v", firstRow.WindowStart, firstRow.WindowEnd)
	}

	if firstRow.ReceivedAt.IsZero() {
		t.Error("first row received_at is zero")
	}

	secondRow := rows[1]
	if secondRow.ObservationPointID != string(device.ObservationPoints[1].ID) {
		t.Errorf("second row observation point = %q, want %q", secondRow.ObservationPointID, device.ObservationPoints[1].ID)
	}

	if secondRow.RateIn != 5 || secondRow.RateOut != 0 {
		t.Errorf("second row rates = %v/%v, want 5/0", secondRow.RateIn, secondRow.RateOut)
	}
}

func TestPostgresMeasurementRepositoryRecordAllEmpty(t *testing.T) {
	repo := NewPostgresMeasurementRepository(testDB)

	if err := repo.RecordAll(context.Background(), "tenant-empty", "event-empty", nil); err != nil {
		t.Fatalf("RecordAll with no measurements: %v", err)
	}

	if rows := selectMeasurements(t, "event-empty"); len(rows) != 0 {
		t.Errorf("got %d measurement rows, want 0", len(rows))
	}
}

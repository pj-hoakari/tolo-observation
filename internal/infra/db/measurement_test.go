package db

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
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
		start, start.Add(30*time.Second), 6, 3, nil)
	if err != nil {
		t.Fatalf("NewMeasurement first: %v", err)
	}

	second, err := domain.NewMeasurement(string(device.ObservationPoints[1].ID),
		start, start.Add(2*time.Minute), 10, 0, nil)
	if err != nil {
		t.Fatalf("NewMeasurement second: %v", err)
	}

	accepted, err := repo.RecordAll(ctx, "tenant-measure", "event-measure", device.ID, []domain.Measurement{first, second})
	if err != nil {
		t.Fatalf("RecordAll: %v", err)
	}

	if accepted != 2 {
		t.Errorf("accepted = %d, want 2", accepted)
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

func TestPostgresMeasurementRepositoryRecordAllKeepsTheFirstEdgeWindow(t *testing.T) {
	repo := NewPostgresMeasurementRepository(testDB)
	ctx := context.Background()
	device := newDevice(t, "tenant-resend", "event-resend", "in", "out")
	start := time.Now().UTC().Truncate(time.Microsecond)

	window := func(pointIndex int, countIn int32) domain.Measurement {
		measurement, err := domain.NewMeasurement(string(device.ObservationPoints[pointIndex].ID),
			start, start.Add(time.Minute), countIn, 0, nil)
		if err != nil {
			t.Fatalf("NewMeasurement: %v", err)
		}

		return measurement
	}

	if _, err := repo.RecordAll(ctx, "tenant-resend", "event-resend", device.ID, []domain.Measurement{window(0, 4)}); err != nil {
		t.Fatalf("RecordAll first: %v", err)
	}

	var logs bytes.Buffer

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	accepted, err := repo.RecordAll(ctx, "tenant-resend", "event-resend", device.ID,
		[]domain.Measurement{window(0, 9), window(1, 2), window(1, 2)})
	if err != nil {
		t.Fatalf("RecordAll resend: %v", err)
	}

	if accepted != 1 {
		t.Errorf("accepted = %d, want 1 for the only new window", accepted)
	}

	if got := strings.Count(logs.String(), "resent measurement differs from the accepted one"); got != 1 {
		t.Errorf("logged %d divergent resends, want 1 for the window resent with another count:\n%s", got, logs.String())
	}

	rows := selectMeasurements(t, "event-resend")
	if len(rows) != 2 || rows[0].CountIn != 4 || rows[1].CountIn != 2 {
		t.Errorf("rows = %+v, want the first window kept at count_in 4 and one row for the new window", rows)
	}

	other := newDevice(t, "tenant-resend", "event-resend", "in")
	if accepted, err := repo.RecordAll(ctx, "tenant-resend", "event-resend", other.ID, []domain.Measurement{window(0, 4)}); err != nil || accepted != 1 {
		t.Errorf("RecordAll from another device = %d, %v, want 1 accepted", accepted, err)
	}
}

func TestPostgresMeasurementRepositoryRecordAllKeepsEveryQRMeasurement(t *testing.T) {
	repo := NewPostgresMeasurementRepository(testDB)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Microsecond)

	measurement, err := domain.NewMeasurement("0123456789abcdef", start, start.Add(time.Second), 1, 0, nil)
	if err != nil {
		t.Fatalf("NewMeasurement: %v", err)
	}

	accepted, err := repo.RecordAll(ctx, "tenant-qr", "event-qr", "", []domain.Measurement{measurement, measurement})
	if err != nil {
		t.Fatalf("RecordAll: %v", err)
	}

	if accepted != 2 || len(selectMeasurements(t, "event-qr")) != 2 {
		t.Errorf("accepted = %d, want both QR measurements kept", accepted)
	}
}

func TestPostgresMeasurementRepositoryRecordAllEmpty(t *testing.T) {
	repo := NewPostgresMeasurementRepository(testDB)

	if _, err := repo.RecordAll(context.Background(), "tenant-empty", "event-empty", "", nil); err != nil {
		t.Fatalf("RecordAll with no measurements: %v", err)
	}

	if rows := selectMeasurements(t, "event-empty"); len(rows) != 0 {
		t.Errorf("got %d measurement rows, want 0", len(rows))
	}
}

func TestPostgresMeasurementRepositoryListWindowEndingSinceIncludesTheBoundary(t *testing.T) {
	repo := NewPostgresMeasurementRepository(testDB)
	ctx := context.Background()
	device := newDevice(t, "tenant-since", "event-since", "corridor")
	since := time.Now().UTC().Truncate(time.Microsecond)

	var measurements []domain.Measurement

	for _, end := range []time.Time{since.Add(-time.Microsecond), since, since.Add(time.Microsecond)} {
		measurement, err := domain.NewMeasurement(string(device.ObservationPoints[0].ID), end.Add(-time.Minute), end, 1, 0, nil)
		if err != nil {
			t.Fatalf("NewMeasurement: %v", err)
		}

		measurements = append(measurements, measurement)
	}

	if _, err := repo.RecordAll(ctx, "tenant-since", "event-since", device.ID, measurements); err != nil {
		t.Fatalf("RecordAll: %v", err)
	}

	listed, err := repo.ListWindowEndingSince(ctx, "event-since", since)
	if err != nil {
		t.Fatalf("ListWindowEndingSince: %v", err)
	}

	if len(listed) != 2 || !listed[0].WindowEnd.Equal(since) || !listed[1].WindowEnd.Equal(since.Add(time.Microsecond)) {
		t.Errorf("listed windows = %+v, want the ones ending at and after %v", listed, since)
	}
}

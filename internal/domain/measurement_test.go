package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

const testObservationPointID = "0123456789abcdef"

func TestNewMeasurement(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Second)

	measurement, err := domain.NewMeasurement(testObservationPointID, start, end, 3, 1)
	if err != nil {
		t.Fatalf("NewMeasurement: %v", err)
	}

	if measurement.ObservationPointID != domain.ObservationPointID(testObservationPointID) {
		t.Errorf("measurement = %+v", measurement)
	}

	if measurement.CountIn != 3 || measurement.CountOut != 1 {
		t.Errorf("measurement = %+v", measurement)
	}
}

func TestNewMeasurementValidation(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)

	tests := []struct {
		name               string
		observationPointID string
		windowStart        time.Time
		windowEnd          time.Time
		countIn            int32
		countOut           int32
		wantErr            error
	}{
		{
			name: "window end equals start", observationPointID: testObservationPointID,
			windowStart: start, windowEnd: start, wantErr: domain.ErrInvalidMeasurementWindow,
		},
		{
			name: "window end before start", observationPointID: testObservationPointID,
			windowStart: end, windowEnd: start, wantErr: domain.ErrInvalidMeasurementWindow,
		},
		{
			name: "negative count in", observationPointID: testObservationPointID,
			windowStart: start, windowEnd: end, countIn: -1, wantErr: domain.ErrNegativeCount,
		},
		{
			name: "negative count out", observationPointID: testObservationPointID,
			windowStart: start, windowEnd: end, countOut: -1, wantErr: domain.ErrNegativeCount,
		},
		{
			name: "without observation point", windowStart: start, windowEnd: end,
			wantErr: domain.ErrInvalidPublicID,
		},
		{
			name: "malformed observation point", observationPointID: "not-an-id",
			windowStart: start, windowEnd: end, wantErr: domain.ErrInvalidPublicID,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := domain.NewMeasurement(test.observationPointID,
				test.windowStart, test.windowEnd, test.countIn, test.countOut)
			if !errors.Is(err, test.wantErr) {
				t.Errorf("NewMeasurement error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestMeasurementRates(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.September, 22, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		window      time.Duration
		countIn     int32
		countOut    int32
		wantRateIn  float64
		wantRateOut float64
	}{
		{name: "one minute window", window: time.Minute, countIn: 12, countOut: 4, wantRateIn: 12, wantRateOut: 4},
		{name: "half minute window", window: 30 * time.Second, countIn: 6, countOut: 3, wantRateIn: 12, wantRateOut: 6},
		{name: "two minute window", window: 2 * time.Minute, countIn: 10, countOut: 0, wantRateIn: 5, wantRateOut: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			measurement, err := domain.NewMeasurement(testObservationPointID,
				start, start.Add(test.window), test.countIn, test.countOut)
			if err != nil {
				t.Fatalf("NewMeasurement: %v", err)
			}

			if measurement.RateIn() != test.wantRateIn {
				t.Errorf("RateIn() = %v, want %v", measurement.RateIn(), test.wantRateIn)
			}

			if measurement.RateOut() != test.wantRateOut {
				t.Errorf("RateOut() = %v, want %v", measurement.RateOut(), test.wantRateOut)
			}
		})
	}
}

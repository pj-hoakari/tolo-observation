package domain

import (
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidMeasurementWindow = errors.New("measurement window end must be after window start")
	ErrNegativeCount            = errors.New("measurement count must not be negative")
	ErrInvalidDetectedPeople    = errors.New("mean detected people must be a finite non-negative number")
)

type Measurement struct {
	ObservationPointID ObservationPointID
	WindowStart        time.Time
	WindowEnd          time.Time
	CountIn            int32
	CountOut           int32
	MeanDetectedPeople *float64
}

func NewMeasurement(
	observationPointID string,
	windowStart, windowEnd time.Time,
	countIn, countOut int32,
	meanDetectedPeople *float64,
) (Measurement, error) {
	pointID, err := ParseObservationPointID(strings.TrimSpace(observationPointID))
	if err != nil {
		return Measurement{}, err
	}

	if !windowEnd.After(windowStart) {
		return Measurement{}, ErrInvalidMeasurementWindow
	}

	if countIn < 0 || countOut < 0 {
		return Measurement{}, ErrNegativeCount
	}

	if meanDetectedPeople != nil && (*meanDetectedPeople < 0 || math.IsNaN(*meanDetectedPeople) || math.IsInf(*meanDetectedPeople, 0)) {
		return Measurement{}, ErrInvalidDetectedPeople
	}

	return Measurement{
		ObservationPointID: pointID,
		WindowStart:        windowStart,
		WindowEnd:          windowEnd,
		CountIn:            countIn,
		CountOut:           countOut,
		MeanDetectedPeople: meanDetectedPeople,
	}, nil
}

func (m Measurement) RateIn() float64 {
	return m.rate(m.CountIn)
}

func (m Measurement) RateOut() float64 {
	return m.rate(m.CountOut)
}

func (m Measurement) rate(count int32) float64 {
	minutes := m.WindowEnd.Sub(m.WindowStart).Minutes()
	if minutes <= 0 {
		return 0
	}

	return float64(count) / minutes
}

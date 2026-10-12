package domain

import "errors"

var (
	ErrInvalidHistoryWindow = errors.New("history window days must be at least 1")
	ErrEventTypeRequired    = errors.New("event type is required")
)

type EventType int

const (
	EventTypeUnspecified EventType = iota
	EventTypeShortTerm
	EventTypeLongTerm
)

const DefaultHistoryWindowDays = 30

type ObservationSettings struct {
	HistoryWindowDays int32
	EventType         EventType
}

func DefaultObservationSettings() ObservationSettings {
	return ObservationSettings{HistoryWindowDays: DefaultHistoryWindowDays, EventType: EventTypeUnspecified}
}

func NewObservationSettings(historyWindowDays int32, eventType EventType) (ObservationSettings, error) {
	if historyWindowDays < 1 {
		return ObservationSettings{}, ErrInvalidHistoryWindow
	}

	if eventType != EventTypeShortTerm && eventType != EventTypeLongTerm {
		return ObservationSettings{}, ErrEventTypeRequired
	}

	return ObservationSettings{HistoryWindowDays: historyWindowDays, EventType: eventType}, nil
}

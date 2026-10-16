package connect

import (
	"context"
	"testing"

	connectrpc "connectrpc.com/connect/v2"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
)

const settingsEventPublicID = "5e77195e77195e77"

func TestObservationSettings(t *testing.T) {
	flow := newObservationFlow(t)
	ctx := context.Background()

	get := func(t *testing.T, eventID string) *observationv1.ObservationSettings {
		t.Helper()

		res, err := flow.settings.GetObservationSettings(authorized(ctx,
			flow.token(t, eventID, "events.read"),
			&observationv1.GetObservationSettingsRequest{EventId: eventID},
		))
		if err != nil {
			t.Fatalf("GetObservationSettings() error = %v", err)
		}

		return res.GetSettings()
	}

	update := func(eventID string, settings *observationv1.ObservationSettings) (*observationv1.ObservationSettings, error) {
		res, err := flow.settings.UpdateObservationSettings(authorized(ctx,
			flow.token(t, eventID, "events.manage"),
			&observationv1.UpdateObservationSettingsRequest{EventId: eventID, Settings: settings},
		))
		if err != nil {
			return nil, err
		}

		return res.GetSettings(), nil
	}

	t.Run("answers the defaults for an event without stored settings", func(t *testing.T) {
		got := get(t, otherEventPublicID)
		if got.GetHistoryWindowDays() != 30 || got.GetEventType() != observationv1.EventType_EVENT_TYPE_UNSPECIFIED {
			t.Errorf("settings = %v, want 30 days and an unspecified event type", got)
		}
	})

	t.Run("stores the settings an update sends", func(t *testing.T) {
		for _, want := range []*observationv1.ObservationSettings{
			{HistoryWindowDays: 7, EventType: observationv1.EventType_EVENT_TYPE_LONG_TERM},
			{HistoryWindowDays: 1, EventType: observationv1.EventType_EVENT_TYPE_SHORT_TERM},
		} {
			updated, err := update(settingsEventPublicID, want)
			if err != nil {
				t.Fatalf("UpdateObservationSettings() error = %v", err)
			}

			if updated.GetHistoryWindowDays() != want.GetHistoryWindowDays() || updated.GetEventType() != want.GetEventType() {
				t.Errorf("updated settings = %v, want %v", updated, want)
			}

			got := get(t, settingsEventPublicID)
			if got.GetHistoryWindowDays() != want.GetHistoryWindowDays() || got.GetEventType() != want.GetEventType() {
				t.Errorf("stored settings = %v, want %v", got, want)
			}
		}
	})

	t.Run("rejects invalid settings", func(t *testing.T) {
		for name, settings := range map[string]*observationv1.ObservationSettings{
			"zero days":        {HistoryWindowDays: 0, EventType: observationv1.EventType_EVENT_TYPE_LONG_TERM},
			"negative days":    {HistoryWindowDays: -1, EventType: observationv1.EventType_EVENT_TYPE_LONG_TERM},
			"unspecified type": {HistoryWindowDays: 30, EventType: observationv1.EventType_EVENT_TYPE_UNSPECIFIED},
			"unknown type":     {HistoryWindowDays: 30, EventType: observationv1.EventType(9)},
			"missing settings": nil,
		} {
			_, err := update(settingsEventPublicID, settings)
			if got := connectrpc.CodeOf(err); got != connectrpc.CodeInvalidArgument {
				t.Errorf("%s: code = %v, want %v (error %v)", name, got, connectrpc.CodeInvalidArgument, err)
			}
		}

		got := get(t, settingsEventPublicID)
		if got.GetHistoryWindowDays() != 1 || got.GetEventType() != observationv1.EventType_EVENT_TYPE_SHORT_TERM {
			t.Errorf("settings after rejected updates = %v, want the last stored 1 day short-term", got)
		}
	})

	t.Run("rejects a token for another event", func(t *testing.T) {
		foreignToken := flow.token(t, otherEventPublicID, "events.read events.manage")

		_, getErr := flow.settings.GetObservationSettings(authorized(ctx,
			foreignToken,
			&observationv1.GetObservationSettingsRequest{EventId: settingsEventPublicID},
		))

		_, updateErr := flow.settings.UpdateObservationSettings(authorized(ctx,
			foreignToken,
			&observationv1.UpdateObservationSettingsRequest{
				EventId:  settingsEventPublicID,
				Settings: &observationv1.ObservationSettings{HistoryWindowDays: 3, EventType: observationv1.EventType_EVENT_TYPE_LONG_TERM},
			},
		))

		for name, err := range map[string]error{"Get": getErr, "Update": updateErr} {
			if got := connectrpc.CodeOf(err); got != connectrpc.CodePermissionDenied {
				t.Errorf("%s: code = %v, want %v (error %v)", name, got, connectrpc.CodePermissionDenied, err)
			}
		}

		got := get(t, settingsEventPublicID)
		if got.GetHistoryWindowDays() != 1 {
			t.Errorf("settings after the foreign update = %v, want them unchanged", got)
		}
	})
}

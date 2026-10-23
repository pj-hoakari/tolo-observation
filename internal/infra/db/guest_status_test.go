package db

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

func TestPostgresGuestStatusRepositoryAdvancesTheSequencePerEvent(t *testing.T) {
	repo := NewPostgresGuestStatusRepository(testDB)
	ctx := context.Background()
	takenAt := time.Date(2026, time.October, 23, 10, 0, 0, 0, time.UTC)

	if _, err := repo.LatestGuestStatus(ctx, "event-guest"); !errors.Is(err, repository.ErrGuestStatusNotFound) {
		t.Fatalf("LatestGuestStatus before any save error = %v, want %v", err, repository.ErrGuestStatusNotFound)
	}

	save := func(eventID string, takenAt time.Time) domain.GuestStatus {
		t.Helper()

		saved, err := repo.SaveGuestStatus(ctx, "tenant-guest", domain.GuestStatus{
			EventID: eventID,
			TakenAt: takenAt,
		})
		if err != nil {
			t.Fatalf("SaveGuestStatus(%s): %v", eventID, err)
		}

		return saved
	}

	first := save("event-guest", takenAt)
	other := save("event-guest-other", takenAt)
	second := save("event-guest", takenAt.Add(time.Minute))

	if second.Sequence != first.Sequence+1 {
		t.Errorf("second sequence = %d, want %d", second.Sequence, first.Sequence+1)
	}

	if again := save("event-guest-other", takenAt.Add(time.Minute)); again.Sequence != other.Sequence+1 {
		t.Errorf("other event sequence = %d, want %d independent of event-guest", again.Sequence, other.Sequence+1)
	}

	latest, err := repo.LatestGuestStatus(ctx, "event-guest")
	if err != nil {
		t.Fatalf("LatestGuestStatus: %v", err)
	}

	if latest.Sequence != second.Sequence || !latest.TakenAt.Equal(takenAt.Add(time.Minute)) {
		t.Errorf("latest = %d at %v, want %d at %v", latest.Sequence, latest.TakenAt, second.Sequence, takenAt.Add(time.Minute))
	}
}

func TestPostgresGuestStatusRepositoryKeepsCrowdAndQueues(t *testing.T) {
	repo := NewPostgresGuestStatusRepository(testDB)
	ctx := context.Background()

	crowd := []domain.CrowdStatus{
		{Anchor: domain.GraphAnchor{PointID: "p1"}, Level: domain.CrowdLevelHigh, Danger: true},
		{Anchor: domain.GraphAnchor{RouteID: "r1"}, Level: domain.CrowdLevelLow},
	}
	queues := []domain.QueueStatus{{
		QueueID:                 "q1",
		GatePointIDs:            []string{"g1", "g2"},
		WaitTimeMinutes:         12.5,
		TurnaroundCount:         3,
		RecommendedGatePointIDs: []string{"g2"},
	}}

	if _, err := repo.SaveGuestStatus(ctx, "tenant-guest", domain.GuestStatus{
		EventID: "event-guest-contents",
		TakenAt: time.Date(2026, time.October, 23, 10, 0, 0, 0, time.UTC),
		Crowd:   crowd,
		Queues:  queues,
	}); err != nil {
		t.Fatalf("SaveGuestStatus: %v", err)
	}

	latest, err := repo.LatestGuestStatus(ctx, "event-guest-contents")
	if err != nil {
		t.Fatalf("LatestGuestStatus: %v", err)
	}

	if !reflect.DeepEqual(latest.Crowd, crowd) || !reflect.DeepEqual(latest.Queues, queues) {
		t.Errorf("latest crowd/queues = %+v / %+v, want %+v / %+v", latest.Crowd, latest.Queues, crowd, queues)
	}
}

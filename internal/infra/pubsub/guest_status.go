package pubsub

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

var crowdLevels = map[domain.CrowdLevel]observationv1.CrowdLevel{
	domain.CrowdLevelUnspecified: observationv1.CrowdLevel_CROWD_LEVEL_UNSPECIFIED,
	domain.CrowdLevelLow:         observationv1.CrowdLevel_CROWD_LEVEL_LOW,
	domain.CrowdLevelMid:         observationv1.CrowdLevel_CROWD_LEVEL_MID,
	domain.CrowdLevelHigh:        observationv1.CrowdLevel_CROWD_LEVEL_HIGH,
}

func GuestStatusProto(status domain.GuestStatus) *observationv1.GuestStatus {
	crowd := make([]*observationv1.CrowdStatus, 0, len(status.Crowd))
	for _, c := range status.Crowd {
		message := &observationv1.CrowdStatus{
			Target: &observationv1.CrowdStatus_PointId{PointId: c.Anchor.PointID},
			Level:  crowdLevels[c.Level],
			Danger: c.Danger,
		}

		if c.Anchor.RouteID != "" {
			message.Target = &observationv1.CrowdStatus_RouteId{RouteId: c.Anchor.RouteID}
		}

		crowd = append(crowd, message)
	}

	queues := make([]*observationv1.QueueStatus, 0, len(status.Queues))
	for _, q := range status.Queues {
		queues = append(queues, &observationv1.QueueStatus{
			QueueId:                 q.QueueID,
			GatePointIds:            q.GatePointIDs,
			WaitTimeMinutes:         q.WaitTimeMinutes,
			TurnaroundCount:         q.TurnaroundCount,
			RecommendedGatePointIds: q.RecommendedGatePointIDs,
		})
	}

	return &observationv1.GuestStatus{
		EventId:  status.EventID,
		Sequence: status.Sequence,
		TakenAt:  timestamppb.New(status.TakenAt),
		Crowd:    crowd,
		Queues:   queues,
	}
}

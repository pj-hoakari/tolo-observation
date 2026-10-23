package domain

import "time"

type CrowdLevel int

const (
	CrowdLevelUnspecified CrowdLevel = iota
	CrowdLevelLow
	CrowdLevelMid
	CrowdLevelHigh
)

type CrowdStatus struct {
	Anchor GraphAnchor
	Level  CrowdLevel
	Danger bool
}

type QueueStatus struct {
	QueueID                 string
	GatePointIDs            []string
	WaitTimeMinutes         float64
	TurnaroundCount         int32
	RecommendedGatePointIDs []string
}

type GuestStatus struct {
	EventID  string
	Sequence uint64
	TakenAt  time.Time
	Crowd    []CrowdStatus
	Queues   []QueueStatus
}

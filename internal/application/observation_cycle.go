package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

const historyWindow = 30 * time.Minute

type GraphSupply interface {
	CurrentGraph(ctx context.Context, eventID string) (domain.Graph, error)
	ObservationPointMappings(ctx context.Context, eventID, revisionID string) ([]domain.ObservationPointMapping, error)
	QrLocations(ctx context.Context, tenantPublicID, eventID, revisionID string) ([]domain.ObservationPointMapping, error)
}

type OptimizeInput struct {
	TenantPublicID   string
	EventType        domain.EventType
	Graph            domain.Graph
	Snapshot         domain.Snapshot
	Gaps             domain.Gaps
	SensorSetChanges []domain.SensorSet
	History          []domain.Snapshot
	ArcHistory       []domain.ArcHistory
	AvailableHistory time.Duration
	Previous         *domain.OptimizationOutcome
	ServerTime       time.Time
}

type FlowControl interface {
	Optimize(ctx context.Context, input OptimizeInput) (domain.OptimizationOutcome, error)
}

type ObservationCycleRunner interface {
	Run(ctx context.Context, tenantPublicID, eventID string) error
}

type ObservationCycleConfig struct {
	Window           time.Duration
	OptimizeTimeout  time.Duration
	HeartbeatTimeout time.Duration
	Now              func() time.Time
}

type ObservationCycle struct {
	graphs       GraphSupply
	flow         FlowControl
	devices      repository.EdgeDeviceRepository
	measurements repository.MeasurementRepository
	snapshots    repository.SnapshotRepository
	settings     repository.ObservationSettingsRepository
	config       ObservationCycleConfig
}

func NewObservationCycle(
	graphs GraphSupply,
	flow FlowControl,
	devices repository.EdgeDeviceRepository,
	measurements repository.MeasurementRepository,
	snapshots repository.SnapshotRepository,
	settings repository.ObservationSettingsRepository,
	config ObservationCycleConfig,
) *ObservationCycle {
	if config.Now == nil {
		config.Now = time.Now
	}

	return &ObservationCycle{
		graphs:       graphs,
		flow:         flow,
		devices:      devices,
		measurements: measurements,
		snapshots:    snapshots,
		settings:     settings,
		config:       config,
	}
}

func (c *ObservationCycle) Run(ctx context.Context, tenantPublicID, eventID string) error {
	graph, err := c.graphs.CurrentGraph(ctx, eventID)
	if err != nil {
		return fmt.Errorf("get current graph: %w", err)
	}

	mappings, err := c.graphs.ObservationPointMappings(ctx, eventID, graph.RevisionID)
	if err != nil {
		return fmt.Errorf("get observation point mappings: %w", err)
	}

	qrLocations, err := c.graphs.QrLocations(ctx, tenantPublicID, eventID, graph.RevisionID)
	if err != nil {
		return fmt.Errorf("get qr locations: %w", err)
	}

	mappings = append(mappings, qrLocations...)

	windowEnd := c.config.Now().Truncate(time.Microsecond)
	windowStart := windowEnd.Add(-c.config.Window)

	measurements, err := c.measurements.ListWindowEndingSince(ctx, eventID, windowEnd.Add(-domain.MaxHold))
	if err != nil {
		return fmt.Errorf("list window measurements: %w", err)
	}

	states, err := c.observationPointStates(ctx, eventID, windowEnd)
	if err != nil {
		return err
	}

	snapshot, gaps, composed, err := domain.NewSnapshot(eventID, windowStart, windowEnd, measurements, mappings, states)
	if err != nil {
		return err
	}

	recorded, err := c.snapshots.ListSensorSets(ctx, eventID)
	if err != nil {
		return fmt.Errorf("list sensor sets: %w", err)
	}

	sensorSets, updated := domain.TrackSensorSets(recorded, composed, windowEnd)

	if err := c.snapshots.SaveSensorSets(ctx, eventID, updated); err != nil {
		return fmt.Errorf("save sensor sets: %w", err)
	}

	settings, err := findObservationSettings(ctx, c.settings, eventID)
	if err != nil {
		return err
	}

	history, err := c.snapshots.ListSnapshotsEndingAfter(ctx, eventID, windowEnd.Add(-historyWindow))
	if err != nil {
		return fmt.Errorf("list history snapshots: %w", err)
	}

	history = domain.HistorySinceSensorSetChanges(history, sensorSets)

	since := windowEnd.Add(-time.Duration(settings.HistoryWindowDays) * 24 * time.Hour)

	arcHistory, err := c.snapshots.SummarizeArcHistory(ctx, eventID, since)
	if err != nil {
		return fmt.Errorf("summarize arc history: %w", err)
	}

	availableHistory, err := c.availableHistory(ctx, eventID, windowEnd)
	if err != nil {
		return err
	}

	if err := c.snapshots.SaveSnapshot(ctx, tenantPublicID, snapshot); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	var (
		previous      *domain.OptimizationOutcome
		previousCycle time.Time
	)

	latest, err := c.snapshots.LatestOptimizationOutcome(ctx, eventID)

	switch {
	case err == nil:
		previous = &latest
		previousCycle = latest.ServerTime
	case !errors.Is(err, repository.ErrOptimizationOutcomeMissing):
		return fmt.Errorf("find previous optimization outcome: %w", err)
	}

	optimizeCtx, cancel := context.WithTimeout(ctx, c.config.OptimizeTimeout)
	defer cancel()

	outcome, err := c.flow.Optimize(optimizeCtx, OptimizeInput{
		TenantPublicID:   tenantPublicID,
		EventType:        settings.EventType,
		Graph:            graph,
		Snapshot:         snapshot,
		Gaps:             gaps,
		SensorSetChanges: domain.SensorSetsChangedAfter(sensorSets, previousCycle),
		History:          history,
		ArcHistory:       arcHistory,
		AvailableHistory: availableHistory,
		Previous:         previous,
		ServerTime:       windowEnd,
	})
	if err != nil {
		return fmt.Errorf("optimize: %w", err)
	}

	outcome.SnapshotID = snapshot.ID

	if err := c.snapshots.SaveOptimizationOutcome(ctx, eventID, outcome); err != nil {
		return fmt.Errorf("save optimization outcome: %w", err)
	}

	return nil
}

func (c *ObservationCycle) availableHistory(ctx context.Context, eventID string, now time.Time) (time.Duration, error) {
	earliest, err := c.snapshots.EarliestSnapshotStart(ctx, eventID)
	if errors.Is(err, repository.ErrSnapshotNotFound) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("find earliest snapshot start: %w", err)
	}

	return now.Sub(earliest), nil
}

func (c *ObservationCycle) observationPointStates(
	ctx context.Context,
	eventID string,
	now time.Time,
) (map[domain.ObservationPointID]domain.ObservationPointState, error) {
	devices, err := c.devices.ListByEvent(ctx, eventID, true)
	if err != nil {
		return nil, fmt.Errorf("list edge devices: %w", err)
	}

	states := map[domain.ObservationPointID]domain.ObservationPointState{}

	for _, device := range devices {
		online := device.Online(now, c.config.HeartbeatTimeout)

		for _, point := range device.ObservationPoints {
			switch {
			case device.Unregistered || !point.Enabled:
				states[point.ID] = domain.ObservationPointDisabled
			case !online:
				states[point.ID] = domain.ObservationPointOffline
			default:
				states[point.ID] = domain.ObservationPointActive
			}
		}
	}

	return states, nil
}

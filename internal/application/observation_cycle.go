package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

const historyWindow = 30 * time.Minute

type GraphSupply interface {
	CurrentGraph(ctx context.Context, eventID string) (domain.Graph, error)
	ObservationPointMappings(ctx context.Context, eventID string) ([]domain.ObservationPointMapping, error)
}

type OptimizeInput struct {
	TenantPublicID string
	Graph          domain.Graph
	Snapshot       domain.Snapshot
	History        []domain.Snapshot
	Previous       *domain.OptimizationOutcome
	ServerTime     time.Time
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
	config       ObservationCycleConfig
}

func NewObservationCycle(
	graphs GraphSupply,
	flow FlowControl,
	devices repository.EdgeDeviceRepository,
	measurements repository.MeasurementRepository,
	snapshots repository.SnapshotRepository,
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
		config:       config,
	}
}

func (c *ObservationCycle) Run(ctx context.Context, tenantPublicID, eventID string) error {
	graph, err := c.graphs.CurrentGraph(ctx, eventID)
	if err != nil {
		return fmt.Errorf("get current graph: %w", err)
	}

	mappings, err := c.graphs.ObservationPointMappings(ctx, eventID)
	if err != nil {
		return fmt.Errorf("get observation point mappings: %w", err)
	}

	windowEnd := c.config.Now()
	windowStart := windowEnd.Add(-c.config.Window)

	measurements, err := c.measurements.ListWindowEndingAfter(ctx, eventID, windowStart)
	if err != nil {
		return fmt.Errorf("list window measurements: %w", err)
	}

	enabled, err := c.enabledObservationPoints(ctx, eventID, windowEnd)
	if err != nil {
		return err
	}

	measurements = slices.DeleteFunc(measurements, func(measurement domain.Measurement) bool {
		return !enabled[measurement.ObservationPointID]
	})

	snapshot, err := domain.NewSnapshot(eventID, windowStart, windowEnd, measurements, mappings)
	if err != nil {
		return err
	}

	history, err := c.snapshots.ListSnapshotsEndingAfter(ctx, eventID, windowEnd.Add(-historyWindow))
	if err != nil {
		return fmt.Errorf("list history snapshots: %w", err)
	}

	if err := c.snapshots.SaveSnapshot(ctx, tenantPublicID, snapshot); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	var previous *domain.OptimizationOutcome

	latest, err := c.snapshots.LatestOptimizationOutcome(ctx, eventID)

	switch {
	case err == nil:
		previous = &latest
	case !errors.Is(err, repository.ErrOptimizationOutcomeMissing):
		return fmt.Errorf("find previous optimization outcome: %w", err)
	}

	optimizeCtx, cancel := context.WithTimeout(ctx, c.config.OptimizeTimeout)
	defer cancel()

	outcome, err := c.flow.Optimize(optimizeCtx, OptimizeInput{
		TenantPublicID: tenantPublicID,
		Graph:          graph,
		Snapshot:       snapshot,
		History:        history,
		Previous:       previous,
		ServerTime:     windowEnd,
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

func (c *ObservationCycle) enabledObservationPoints(
	ctx context.Context,
	eventID string,
	now time.Time,
) (map[domain.ObservationPointID]bool, error) {
	devices, err := c.devices.ListByEvent(ctx, eventID, false)
	if err != nil {
		return nil, fmt.Errorf("list edge devices: %w", err)
	}

	enabled := map[domain.ObservationPointID]bool{}

	for _, device := range devices {
		if !device.Online(now, c.config.HeartbeatTimeout) {
			continue
		}

		for _, point := range device.ObservationPoints {
			if point.Enabled {
				enabled[point.ID] = true
			}
		}
	}

	return enabled, nil
}

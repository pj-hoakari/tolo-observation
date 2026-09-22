package application

//go:generate go tool mockgen -source=../repository/edge_device.go -destination=mock_edge_device_repository_test.go -package=application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

var ErrForeignEvent = errors.New("resource belongs to another event")

type RegisterEdgeDeviceInput struct {
	EventID               string
	Name                  string
	ObservationPointNames []string
}

type RegisterEdgeDeviceOutput struct {
	Device             domain.EdgeDevice
	ObservationPageURL string
}

type UnregisterEdgeDeviceInput struct {
	EventID      string
	EdgeDeviceID string
}

type ListEdgeDevicesInput struct {
	EventID             string
	IncludeUnregistered bool
}

type HeartbeatInput struct {
	EventID                   string
	EdgeDeviceID              string
	ActiveObservationPointIDs []string
}

type UpdateObservationPointConfigInput struct {
	EventID            string
	ObservationPointID string
	Name               string
	Enabled            bool
}

type EdgeDeviceUseCases interface {
	RegisterEdgeDevice(ctx context.Context, input RegisterEdgeDeviceInput) (RegisterEdgeDeviceOutput, error)
	UnregisterEdgeDevice(ctx context.Context, input UnregisterEdgeDeviceInput) (domain.EdgeDevice, error)
	ListEdgeDevices(ctx context.Context, input ListEdgeDevicesInput) ([]domain.EdgeDevice, error)
	Heartbeat(ctx context.Context, input HeartbeatInput) error
	UpdateObservationPointConfig(ctx context.Context, input UpdateObservationPointConfigInput) (domain.ObservationPoint, error)
}

type EdgeDeviceConfig struct {
	ObservationPageBaseURL string
	HeartbeatTimeout       time.Duration
	Now                    func() time.Time
}

type EdgeDeviceService struct {
	devices repository.EdgeDeviceRepository
	config  EdgeDeviceConfig
}

func NewEdgeDeviceService(devices repository.EdgeDeviceRepository, config EdgeDeviceConfig) *EdgeDeviceService {
	if config.Now == nil {
		config.Now = time.Now
	}

	return &EdgeDeviceService{devices: devices, config: config}
}

func (s *EdgeDeviceService) RegisterEdgeDevice(ctx context.Context, input RegisterEdgeDeviceInput) (RegisterEdgeDeviceOutput, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return RegisterEdgeDeviceOutput{}, err
	}

	tenantPublicID, ok := tenantctx.TenantPublicIDFromContext(ctx)
	if !ok {
		return RegisterEdgeDeviceOutput{}, tenantctx.ErrMissing
	}

	device, err := domain.NewEdgeDevice(tenantPublicID, input.EventID, input.Name, input.ObservationPointNames)
	if err != nil {
		return RegisterEdgeDeviceOutput{}, err
	}

	if err := s.devices.Create(ctx, device); err != nil {
		return RegisterEdgeDeviceOutput{}, fmt.Errorf("create edge device: %w", err)
	}

	return RegisterEdgeDeviceOutput{
		Device:             device,
		ObservationPageURL: strings.TrimRight(s.config.ObservationPageBaseURL, "/") + "/" + string(device.ID),
	}, nil
}

func (s *EdgeDeviceService) UnregisterEdgeDevice(ctx context.Context, input UnregisterEdgeDeviceInput) (domain.EdgeDevice, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return domain.EdgeDevice{}, err
	}

	id, err := domain.ParseEdgeDeviceID(input.EdgeDeviceID)
	if err != nil {
		return domain.EdgeDevice{}, err
	}

	device, err := s.devices.FindByID(ctx, id)
	if err != nil {
		return domain.EdgeDevice{}, fmt.Errorf("find edge device: %w", err)
	}

	if device.EventID != input.EventID {
		return domain.EdgeDevice{}, ErrForeignEvent
	}

	if err := device.Unregister(); err != nil {
		return domain.EdgeDevice{}, err
	}

	if err := s.devices.Save(ctx, device); err != nil {
		return domain.EdgeDevice{}, fmt.Errorf("save edge device: %w", err)
	}

	return device, nil
}

func (s *EdgeDeviceService) ListEdgeDevices(ctx context.Context, input ListEdgeDevicesInput) ([]domain.EdgeDevice, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return nil, err
	}

	devices, err := s.devices.ListByEvent(ctx, input.EventID, input.IncludeUnregistered)
	if err != nil {
		return nil, fmt.Errorf("list edge devices: %w", err)
	}

	for i := range devices {
		s.applyEffectiveEnabled(&devices[i])
	}

	return devices, nil
}

func (s *EdgeDeviceService) Heartbeat(ctx context.Context, input HeartbeatInput) error {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return err
	}

	id, err := domain.ParseEdgeDeviceID(input.EdgeDeviceID)
	if err != nil {
		return err
	}

	device, err := s.devices.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("find edge device: %w", err)
	}

	if device.EventID != input.EventID {
		return ErrForeignEvent
	}

	activeIDs := make([]domain.ObservationPointID, 0, len(input.ActiveObservationPointIDs))

	for _, value := range input.ActiveObservationPointIDs {
		pointID, err := domain.ParseObservationPointID(value)
		if err != nil {
			return err
		}

		activeIDs = append(activeIDs, pointID)
	}

	if err := device.Heartbeat(s.config.Now(), activeIDs); err != nil {
		return err
	}

	if err := s.devices.SaveHeartbeat(ctx, device); err != nil {
		return fmt.Errorf("save edge device heartbeat: %w", err)
	}

	return nil
}

func (s *EdgeDeviceService) UpdateObservationPointConfig(ctx context.Context, input UpdateObservationPointConfigInput) (domain.ObservationPoint, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return domain.ObservationPoint{}, err
	}

	id, err := domain.ParseObservationPointID(input.ObservationPointID)
	if err != nil {
		return domain.ObservationPoint{}, err
	}

	device, err := s.devices.FindByObservationPointID(ctx, id)
	if err != nil {
		return domain.ObservationPoint{}, fmt.Errorf("find edge device by observation point: %w", err)
	}

	if device.EventID != input.EventID {
		return domain.ObservationPoint{}, ErrForeignEvent
	}

	if _, err := device.UpdateObservationPoint(id, input.Name, input.Enabled); err != nil {
		return domain.ObservationPoint{}, err
	}

	if err := s.devices.Save(ctx, device); err != nil {
		return domain.ObservationPoint{}, fmt.Errorf("save edge device: %w", err)
	}

	s.applyEffectiveEnabled(&device)

	for _, point := range device.ObservationPoints {
		if point.ID == id {
			return point, nil
		}
	}

	return domain.ObservationPoint{}, domain.ErrObservationPointNotFound
}

func (s *EdgeDeviceService) applyEffectiveEnabled(device *domain.EdgeDevice) {
	online := device.Online(s.config.Now(), s.config.HeartbeatTimeout)

	for i := range device.ObservationPoints {
		device.ObservationPoints[i].Enabled = device.ObservationPoints[i].Enabled && online
	}
}

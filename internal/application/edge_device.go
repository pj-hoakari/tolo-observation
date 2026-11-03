package application

//go:generate go tool mockgen -source=../repository/edge_device.go -destination=mock_edge_device_repository_test.go -package=application_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

var ErrForeignEvent = errors.New("resource belongs to another event")

type RegisterEdgeDeviceInput struct {
	EventID                  string
	Name                     string
	ObservationPointNames    []string
	Kind                     domain.EdgeDeviceKind
	LinkedObservationPointID string
}

type EdgeDeviceView struct {
	Device domain.EdgeDevice
	Online bool
}

type RegisterEdgeDeviceOutput struct {
	Device              EdgeDeviceView
	ObservationPagePath string
}

type UnregisterEdgeDeviceInput struct {
	EventID      string
	EdgeDeviceID string
}

type ListEdgeDevicesInput struct {
	EventID             string
	IncludeUnregistered bool
}

type ListEdgeDeviceObservationPointsInput struct {
	EventID      string
	EdgeDeviceID string
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
	UnregisterEdgeDevice(ctx context.Context, input UnregisterEdgeDeviceInput) (EdgeDeviceView, error)
	ListEdgeDevices(ctx context.Context, input ListEdgeDevicesInput) ([]EdgeDeviceView, error)
	ListEdgeDeviceObservationPoints(ctx context.Context, input ListEdgeDeviceObservationPointsInput) ([]domain.ObservationPoint, error)
	Heartbeat(ctx context.Context, input HeartbeatInput) error
	UpdateObservationPointConfig(ctx context.Context, input UpdateObservationPointConfigInput) (domain.ObservationPoint, error)
}

type EdgeDeviceConfig struct {
	HeartbeatTimeout time.Duration
	Now              func() time.Time
}

type EdgeDeviceService struct {
	devices repository.EdgeDeviceRepository
	holds   *HeartbeatLossHolds
	config  EdgeDeviceConfig
}

func NewEdgeDeviceService(
	devices repository.EdgeDeviceRepository,
	holds *HeartbeatLossHolds,
	config EdgeDeviceConfig,
) *EdgeDeviceService {
	if config.Now == nil {
		config.Now = time.Now
	}

	return &EdgeDeviceService{devices: devices, holds: holds, config: config}
}

func (s *EdgeDeviceService) RegisterEdgeDevice(ctx context.Context, input RegisterEdgeDeviceInput) (RegisterEdgeDeviceOutput, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return RegisterEdgeDeviceOutput{}, err
	}

	tenantPublicID, ok := tenantctx.TenantPublicIDFromContext(ctx)
	if !ok {
		return RegisterEdgeDeviceOutput{}, tenantctx.ErrMissing
	}

	device, err := domain.NewEdgeDeviceWithKind(
		tenantPublicID,
		input.EventID,
		input.Name,
		input.Kind,
		input.ObservationPointNames,
		input.LinkedObservationPointID,
	)
	if err != nil {
		return RegisterEdgeDeviceOutput{}, err
	}

	create := func(ctx context.Context) error {
		if device.Kind == domain.EdgeDeviceKindCamera {
			if err := s.validateCameraLink(ctx, input.EventID, device.CameraLinks[0]); err != nil {
				return err
			}
		}

		if err := s.devices.Create(ctx, device); err != nil {
			return fmt.Errorf("create edge device: %w", err)
		}

		return nil
	}

	if device.Kind == domain.EdgeDeviceKindCamera {
		err = s.devices.WithinTransaction(ctx, create)
	} else {
		err = create(ctx)
	}

	if err != nil {
		return RegisterEdgeDeviceOutput{}, err
	}

	output := RegisterEdgeDeviceOutput{Device: s.view(device), ObservationPagePath: ""}
	if device.Kind == domain.EdgeDeviceKindObservationPage {
		output.ObservationPagePath = "/event/" + device.EventID + "/observation/" + string(device.ID)
	}

	return output, nil
}

func (s *EdgeDeviceService) validateCameraLink(ctx context.Context, eventID string, link domain.CameraLink) error {
	owner, err := s.devices.FindByObservationPointIDForUpdate(ctx, link.ObservationPointID)
	if err != nil {
		return fmt.Errorf("find linked observation point: %w", err)
	}

	if owner.EventID != eventID {
		return ErrForeignEvent
	}

	if owner.Kind == domain.EdgeDeviceKindCamera {
		return domain.ErrCameraLinkToCamera
	}

	if owner.Unregistered {
		return domain.ErrEdgeDeviceUnregistered
	}

	for _, existing := range owner.CameraLinks {
		if existing.ObservationPointID == link.ObservationPointID {
			return domain.ErrObservationPointAlreadyLinked
		}
	}

	return nil
}

func (s *EdgeDeviceService) UnregisterEdgeDevice(ctx context.Context, input UnregisterEdgeDeviceInput) (EdgeDeviceView, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return EdgeDeviceView{}, err
	}

	id, err := domain.ParseEdgeDeviceID(input.EdgeDeviceID)
	if err != nil {
		return EdgeDeviceView{}, err
	}

	var view EdgeDeviceView

	err = s.devices.WithinTransaction(ctx, func(ctx context.Context) error {
		device, err := s.devices.FindByIDForUpdate(ctx, id)
		if err != nil {
			return fmt.Errorf("find edge device: %w", err)
		}

		if device.EventID != input.EventID {
			return ErrForeignEvent
		}

		if err := device.Unregister(); err != nil {
			return err
		}

		if err := s.devices.Save(ctx, device); err != nil {
			return fmt.Errorf("save edge device: %w", err)
		}

		view = s.view(device)

		return nil
	})
	if err != nil {
		return EdgeDeviceView{}, err
	}

	return view, nil
}

func (s *EdgeDeviceService) ListEdgeDevices(ctx context.Context, input ListEdgeDevicesInput) ([]EdgeDeviceView, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return nil, err
	}

	devices, err := s.devices.ListByEvent(ctx, input.EventID, input.IncludeUnregistered)
	if err != nil {
		return nil, fmt.Errorf("list edge devices: %w", err)
	}

	now := s.config.Now()

	hold, err := s.holds.Evaluate(ctx, now)
	if err != nil {
		return nil, err
	}

	views := make([]EdgeDeviceView, 0, len(devices))

	for _, device := range devices {
		view := s.view(device)
		applyEffectiveEnabled(&view.Device, hold.Connected(device, now))
		views = append(views, view)
	}

	return views, nil
}

func (s *EdgeDeviceService) ListEdgeDeviceObservationPoints(
	ctx context.Context,
	input ListEdgeDeviceObservationPointsInput,
) ([]domain.ObservationPoint, error) {
	if err := tenantctx.EnsureEvent(ctx, input.EventID); err != nil {
		return nil, err
	}

	id, err := domain.ParseEdgeDeviceID(input.EdgeDeviceID)
	if err != nil {
		return nil, err
	}

	device, err := s.devices.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("find edge device: %w", err)
	}

	if device.EventID != input.EventID {
		return nil, ErrForeignEvent
	}

	if device.Kind == domain.EdgeDeviceKindCamera {
		return nil, domain.ErrEdgeDeviceCameraUnsupported
	}

	if device.Unregistered {
		return nil, domain.ErrEdgeDeviceUnregistered
	}

	now := s.config.Now()

	hold, err := s.holds.Evaluate(ctx, now)
	if err != nil {
		return nil, err
	}

	applyEffectiveEnabled(&device, hold.Connected(device, now))

	return device.ObservationPoints, nil
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

	if device.Kind == domain.EdgeDeviceKindCamera {
		return domain.ErrEdgeDeviceCameraUnsupported
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

	now := s.config.Now()

	hold, err := s.holds.Evaluate(ctx, now)
	if err != nil {
		return domain.ObservationPoint{}, err
	}

	applyEffectiveEnabled(&device, hold.Connected(device, now))

	for _, point := range device.ObservationPoints {
		if point.ID == id {
			return point, nil
		}
	}

	return domain.ObservationPoint{}, domain.ErrObservationPointNotFound
}

func (s *EdgeDeviceService) view(device domain.EdgeDevice) EdgeDeviceView {
	return EdgeDeviceView{Device: device, Online: device.Online(s.config.Now(), s.config.HeartbeatTimeout)}
}

func applyEffectiveEnabled(device *domain.EdgeDevice, connected bool) {
	for i := range device.ObservationPoints {
		device.ObservationPoints[i].Enabled = device.ObservationPoints[i].Enabled && connected
	}
}

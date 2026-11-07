package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidPublicID               = errors.New("invalid public ID")
	ErrTenantRequired                = errors.New("tenant public ID is required")
	ErrEventRequired                 = errors.New("event ID is required")
	ErrEdgeDeviceNameRequired        = errors.New("edge device name is required")
	ErrEdgeDeviceUnregistered        = errors.New("edge device is unregistered")
	ErrEdgeDeviceKindInvalid         = errors.New("invalid edge device kind")
	ErrEdgeDeviceCameraUnsupported   = errors.New("camera edge device does not support this operation")
	ErrCameraPointNamesNotAllowed    = errors.New("camera edge device must not have observation points")
	ErrCameraLinkedPointRequired     = errors.New("camera edge device requires one linked observation point")
	ErrPageLinkedPointNotAllowed     = errors.New("observation page edge device must not have a linked observation point")
	ErrCameraLinkToCamera            = errors.New("a camera cannot be linked to another camera")
	ErrObservationPointNameRequired  = errors.New("observation point name is required")
	ErrObservationPointNotFound      = errors.New("observation point not found")
	ErrObservationPointAlreadyLinked = errors.New("observation point is already linked to a camera")
)

const (
	publicIDByteLength = 8
	publicIDLength     = publicIDByteLength * 2
)

func newPublicID() (string, error) {
	buffer := make([]byte, publicIDByteLength)

	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate public ID: %w", err)
	}

	return hex.EncodeToString(buffer), nil
}

func parsePublicID(value string) (string, error) {
	if len(value) != publicIDLength || value != strings.ToLower(value) {
		return "", ErrInvalidPublicID
	}

	if _, err := hex.DecodeString(value); err != nil {
		return "", ErrInvalidPublicID
	}

	return value, nil
}

type EdgeDeviceID string

func NewEdgeDeviceID() (EdgeDeviceID, error) {
	id, err := newPublicID()
	if err != nil {
		return "", err
	}

	return EdgeDeviceID(id), nil
}

func ParseEdgeDeviceID(value string) (EdgeDeviceID, error) {
	id, err := parsePublicID(value)
	if err != nil {
		return "", err
	}

	return EdgeDeviceID(id), nil
}

type ObservationPointID string

func NewObservationPointID() (ObservationPointID, error) {
	id, err := newPublicID()
	if err != nil {
		return "", err
	}

	return ObservationPointID(id), nil
}

func ParseObservationPointID(value string) (ObservationPointID, error) {
	id, err := parsePublicID(value)
	if err != nil {
		return "", err
	}

	return ObservationPointID(id), nil
}

type ObservationPoint struct {
	ID           ObservationPointID
	Name         string
	Enabled      bool
	LastActiveAt *time.Time
}

type EdgeDeviceKind int

const (
	EdgeDeviceKindObservationPage EdgeDeviceKind = iota
	EdgeDeviceKindCamera
)

func ParseEdgeDeviceKind(value string) (EdgeDeviceKind, error) {
	switch value {
	case "observation_page":
		return EdgeDeviceKindObservationPage, nil
	case "camera":
		return EdgeDeviceKindCamera, nil
	default:
		return EdgeDeviceKindObservationPage, ErrEdgeDeviceKindInvalid
	}
}

func (k EdgeDeviceKind) String() string {
	switch k {
	case EdgeDeviceKindCamera:
		return "camera"
	case EdgeDeviceKindObservationPage:
		return "observation_page"
	default:
		return ""
	}
}

type CameraLink struct {
	CameraEdgeDeviceID EdgeDeviceID
	ObservationPointID ObservationPointID
}

type EdgeDevice struct {
	ID                EdgeDeviceID
	TenantPublicID    string
	EventID           string
	Name              string
	Unregistered      bool
	LastHeartbeatAt   *time.Time
	Kind              EdgeDeviceKind
	ObservationPoints []ObservationPoint
	CameraLinks       []CameraLink
}

func NewEdgeDevice(tenantPublicID, eventID, name string, observationPointNames []string) (EdgeDevice, error) {
	return NewEdgeDeviceWithKind(
		tenantPublicID,
		eventID,
		name,
		EdgeDeviceKindObservationPage,
		observationPointNames,
		"",
	)
}

func NewEdgeDeviceWithKind(
	tenantPublicID, eventID, name string,
	kind EdgeDeviceKind,
	observationPointNames []string,
	linkedObservationPointID string,
) (EdgeDevice, error) {
	tenantPublicID = strings.TrimSpace(tenantPublicID)
	if tenantPublicID == "" {
		return EdgeDevice{}, ErrTenantRequired
	}

	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return EdgeDevice{}, ErrEventRequired
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return EdgeDevice{}, ErrEdgeDeviceNameRequired
	}

	linkedObservationPointID = strings.TrimSpace(linkedObservationPointID)

	var observationPoints []ObservationPoint

	var cameraLinks []CameraLink

	switch kind {
	case EdgeDeviceKindObservationPage:
		if linkedObservationPointID != "" {
			return EdgeDevice{}, ErrPageLinkedPointNotAllowed
		}

		observationPoints = make([]ObservationPoint, 0, len(observationPointNames))

		for _, pointName := range observationPointNames {
			pointName = strings.TrimSpace(pointName)
			if pointName == "" {
				return EdgeDevice{}, ErrObservationPointNameRequired
			}

			pointID, err := NewObservationPointID()
			if err != nil {
				return EdgeDevice{}, err
			}

			observationPoints = append(observationPoints, ObservationPoint{
				ID:           pointID,
				Name:         pointName,
				Enabled:      true,
				LastActiveAt: nil,
			})
		}
	case EdgeDeviceKindCamera:
		if len(observationPointNames) != 0 {
			return EdgeDevice{}, ErrCameraPointNamesNotAllowed
		}

		if linkedObservationPointID == "" {
			return EdgeDevice{}, ErrCameraLinkedPointRequired
		}

		linkedPointID, err := ParseObservationPointID(linkedObservationPointID)
		if err != nil {
			return EdgeDevice{}, err
		}

		id, err := NewEdgeDeviceID()
		if err != nil {
			return EdgeDevice{}, err
		}

		return EdgeDevice{
			ID:                id,
			TenantPublicID:    tenantPublicID,
			EventID:           eventID,
			Name:              name,
			Unregistered:      false,
			LastHeartbeatAt:   nil,
			Kind:              EdgeDeviceKindCamera,
			ObservationPoints: []ObservationPoint{},
			CameraLinks: []CameraLink{{
				CameraEdgeDeviceID: id,
				ObservationPointID: linkedPointID,
			}},
		}, nil
	default:
		return EdgeDevice{}, ErrEdgeDeviceKindInvalid
	}

	id, err := NewEdgeDeviceID()
	if err != nil {
		return EdgeDevice{}, err
	}

	return EdgeDevice{
		ID:                id,
		TenantPublicID:    tenantPublicID,
		EventID:           eventID,
		Name:              name,
		Unregistered:      false,
		LastHeartbeatAt:   nil,
		Kind:              kind,
		ObservationPoints: observationPoints,
		CameraLinks:       cameraLinks,
	}, nil
}

func (d *EdgeDevice) Unregister() error {
	if d.Unregistered {
		return ErrEdgeDeviceUnregistered
	}

	d.Unregistered = true

	if d.Kind == EdgeDeviceKindCamera {
		d.CameraLinks = nil

		return nil
	}

	for i := range d.ObservationPoints {
		d.ObservationPoints[i].Enabled = false
	}

	return nil
}

func (d *EdgeDevice) UpdateObservationPoint(id ObservationPointID, name string, enabled bool) (ObservationPoint, error) {
	if d.Unregistered {
		return ObservationPoint{}, ErrEdgeDeviceUnregistered
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return ObservationPoint{}, ErrObservationPointNameRequired
	}

	for i := range d.ObservationPoints {
		if d.ObservationPoints[i].ID != id {
			continue
		}

		d.ObservationPoints[i].Name = name
		d.ObservationPoints[i].Enabled = enabled

		return d.ObservationPoints[i], nil
	}

	return ObservationPoint{}, ErrObservationPointNotFound
}

func (d EdgeDevice) Online(now time.Time, heartbeatTimeout time.Duration) bool {
	if d.Kind == EdgeDeviceKindCamera || d.Unregistered || d.LastHeartbeatAt == nil {
		return false
	}

	return now.Sub(*d.LastHeartbeatAt) <= heartbeatTimeout
}

func (d EdgeDevice) HasObservationPoint(id ObservationPointID) bool {
	for _, point := range d.ObservationPoints {
		if point.ID == id {
			return true
		}
	}

	return false
}

func (d *EdgeDevice) Heartbeat(now time.Time, activeIDs []ObservationPointID) error {
	if d.Kind == EdgeDeviceKindCamera {
		return ErrEdgeDeviceCameraUnsupported
	}

	if d.Unregistered {
		return ErrEdgeDeviceUnregistered
	}

	for _, id := range activeIDs {
		if !d.HasObservationPoint(id) {
			return ErrObservationPointNotFound
		}
	}

	heartbeatAt := now
	d.LastHeartbeatAt = &heartbeatAt

	for i := range d.ObservationPoints {
		if !slices.Contains(activeIDs, d.ObservationPoints[i].ID) {
			continue
		}

		activeAt := now
		d.ObservationPoints[i].LastActiveAt = &activeAt
	}

	return nil
}

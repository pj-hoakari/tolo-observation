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
	ErrInvalidPublicID              = errors.New("invalid public ID")
	ErrTenantRequired               = errors.New("tenant public ID is required")
	ErrEventRequired                = errors.New("event ID is required")
	ErrEdgeDeviceNameRequired       = errors.New("edge device name is required")
	ErrEdgeDeviceUnregistered       = errors.New("edge device is unregistered")
	ErrObservationPointNameRequired = errors.New("observation point name is required")
	ErrObservationPointNotFound     = errors.New("observation point not found")
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

type EdgeDevice struct {
	ID                EdgeDeviceID
	TenantPublicID    string
	EventID           string
	Name              string
	Unregistered      bool
	LastHeartbeatAt   *time.Time
	ObservationPoints []ObservationPoint
}

func NewEdgeDevice(tenantPublicID, eventID, name string, observationPointNames []string) (EdgeDevice, error) {
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

	observationPoints := make([]ObservationPoint, 0, len(observationPointNames))

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
		ObservationPoints: observationPoints,
	}, nil
}

func (d *EdgeDevice) Unregister() error {
	if d.Unregistered {
		return ErrEdgeDeviceUnregistered
	}

	d.Unregistered = true

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
	if d.Unregistered || d.LastHeartbeatAt == nil {
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

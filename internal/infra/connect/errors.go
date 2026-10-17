package connect

import (
	"context"
	"errors"
	"log/slog"

	connectrpc "connectrpc.com/connect/v2"

	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

// errInternal is the only detail a client learns about an internal failure.
var errInternal = errors.New("internal error")

var (
	errMeasurementSourceRequired  = errors.New("measurement source is required")
	errQRLocationNotAllowed       = errors.New("qr_location_id is not allowed for edge measurements")
	errObservationPointNotAllowed = errors.New("observation_point_id is not allowed for QR measurements")
	errEdgeDeviceNotAllowed       = errors.New("edge_device_id is not allowed for QR measurements")
	errMixedMeasurementSources    = errors.New("measurements of one request must share a source")
)

var clientErrors = []struct {
	target  error
	code    connectrpc.Code
	message string
}{
	{errMeasurementSourceRequired, connectrpc.CodeInvalidArgument, "measurement source is required"},
	{errQRLocationNotAllowed, connectrpc.CodeInvalidArgument, "qr_location_id is not allowed for edge measurements"},
	{errObservationPointNotAllowed, connectrpc.CodeInvalidArgument, "observation_point_id is not allowed for QR measurements"},
	{errEdgeDeviceNotAllowed, connectrpc.CodeInvalidArgument, "edge_device_id is not allowed for QR measurements"},
	{errMixedMeasurementSources, connectrpc.CodeInvalidArgument, "measurements of one request must share a source"},
	{domain.ErrInvalidPublicID, connectrpc.CodeInvalidArgument, "invalid public ID"},
	{domain.ErrTenantRequired, connectrpc.CodeInvalidArgument, "tenant_id is required"},
	{domain.ErrEventRequired, connectrpc.CodeInvalidArgument, "event_id is required"},
	{domain.ErrEdgeDeviceNameRequired, connectrpc.CodeInvalidArgument, "edge device name is required"},
	{domain.ErrObservationPointNameRequired, connectrpc.CodeInvalidArgument, "observation point name is required"},
	{domain.ErrInvalidMeasurementWindow, connectrpc.CodeInvalidArgument, "measurement window end must be after window start"},
	{domain.ErrNegativeCount, connectrpc.CodeInvalidArgument, "measurement count must not be negative"},
	{domain.ErrInvalidDetectedPeople, connectrpc.CodeInvalidArgument, "mean detected people must be a finite non-negative number"},
	{domain.ErrInvalidHistoryWindow, connectrpc.CodeInvalidArgument, "history_window_days must be at least 1"},
	{domain.ErrEventTypeRequired, connectrpc.CodeInvalidArgument, "event_type is required"},
	{application.ErrEdgeDeviceRequired, connectrpc.CodeInvalidArgument, "edge_device_id is required for edge measurements"},
	{repository.ErrEdgeDeviceNotFound, connectrpc.CodeNotFound, "edge device not found"},
	{domain.ErrObservationPointNotFound, connectrpc.CodeNotFound, "observation point not found"},
	{domain.ErrEdgeDeviceUnregistered, connectrpc.CodeFailedPrecondition, "edge device is unregistered"},
	{tenantctx.ErrMissing, connectrpc.CodePermissionDenied, "tenant ID is missing from the token"},
	{tenantctx.ErrMismatch, connectrpc.CodePermissionDenied, "tenant ID does not match the token"},
	{tenantctx.ErrEventMissing, connectrpc.CodePermissionDenied, "event ID is missing from the token"},
	{tenantctx.ErrEventMismatch, connectrpc.CodePermissionDenied, "event ID does not match the token"},
	{application.ErrForeignEvent, connectrpc.CodePermissionDenied, "resource belongs to another event"},
}

func clientError(ctx context.Context, err error) *connectrpc.Error {
	for _, candidate := range clientErrors {
		if errors.Is(err, candidate.target) {
			return connectrpc.NewError(candidate.code, candidate.message).WithCause(err)
		}
	}

	return InternalError(ctx, err)
}

// InternalError reports a failure the client can do nothing about. The cause is
// written to the server log and replaced by a fixed message, so that no
// internal detail leaves the service. The log handler names the trace of the
// request context on the record, so an operator can find the failure in the
// trace it belongs to.
//
// A cancelled or timed-out request is the client going away rather than a
// server fault, so it keeps its own code and is not logged.
//
// It is exported for the other transports of this process, so that every
// service answers an internal failure the same way.
func InternalError(ctx context.Context, err error) *connectrpc.Error {
	if errors.Is(err, context.Canceled) {
		return connectrpc.NewError(connectrpc.CodeCanceled, "canceled").WithCause(err)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return connectrpc.NewError(connectrpc.CodeDeadlineExceeded, "deadline exceeded").WithCause(err)
	}

	slog.ErrorContext(ctx, "internal error", "error", err)

	return connectrpc.NewError(connectrpc.CodeInternal, errInternal.Error()).WithCause(errInternal) //nolint:forbidigo // the one place that builds internal errors
}

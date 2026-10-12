package connect

import (
	"context"
	"errors"
	"fmt"
	"time"

	connectrpc "connectrpc.com/connect"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

type TenantClient struct {
	client tenantv1connect.TenantServiceClient
}

func NewTenantClient(httpClient connectrpc.HTTPClient, baseURL string) *TenantClient {
	return &TenantClient{
		client: tenantv1connect.NewTenantServiceClient(
			httpClient, baseURL, connectrpc.WithInterceptors(forwardAuthorization()),
		),
	}
}

func (c *TenantClient) ObservationSettings(ctx context.Context, eventID string) (domain.ObservationSettings, error) {
	res, err := c.client.GetObservationSettings(
		ctx, connectrpc.NewRequest(&tenantv1.GetObservationSettingsRequest{EventId: eventID}),
	)
	if err != nil {
		return domain.ObservationSettings{}, fmt.Errorf("call GetObservationSettings: %w", err)
	}

	settings := res.Msg.GetSettings()
	if settings == nil {
		return domain.ObservationSettings{}, errors.New("GetObservationSettings returned no settings")
	}

	days := settings.GetHistoryWindowDays()
	if days <= 0 {
		return domain.ObservationSettings{}, fmt.Errorf("GetObservationSettings returned history_window_days %d", days)
	}

	return domain.ObservationSettings{HistoryWindow: time.Duration(days) * 24 * time.Hour}, nil
}

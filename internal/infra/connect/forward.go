package connect

import (
	"context"

	connectrpc "connectrpc.com/connect/v2"
)

func forwardAuthorization() connectrpc.ClientInterceptor {
	return func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
		return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
			server, ok := connectrpc.CallInfoForServerContext(ctx)
			if !ok {
				return next(ctx, spec)
			}

			if authorization := server.RequestHeader().Get("Authorization"); authorization != "" {
				if client, ok := connectrpc.CallInfoForClientContext(ctx); ok {
					client.RequestHeader().Set("Authorization", authorization)
				}
			}

			return next(ctx, spec)
		}
	}
}

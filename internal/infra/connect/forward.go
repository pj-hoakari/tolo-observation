package connect

import (
	"context"

	connectrpc "connectrpc.com/connect"
)

type authorizationKey struct{}

func forwardAuthorization() connectrpc.UnaryInterceptorFunc {
	return func(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
		return func(ctx context.Context, req connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
			if !req.Spec().IsClient {
				return next(context.WithValue(ctx, authorizationKey{}, req.Header().Get("Authorization")), req)
			}

			if authorization, ok := ctx.Value(authorizationKey{}).(string); ok && authorization != "" {
				req.Header().Set("Authorization", authorization)
			}

			return next(ctx, req)
		}
	}
}

package tenantctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"

	"github.com/pj-hoakari/tolo-observation/internal/tenantctx"
)

// withTenant returns a context carrying verified claims that authenticate the
// given tenant, as the transport interceptor would.
func withTenant(ctx context.Context, tenantPublicID string) context.Context {
	return internaljwt.ContextWithClaims(ctx, internaljwt.Claims{TenantPublicID: tenantPublicID})
}

// withSubject returns a context carrying verified claims that authenticate the
// given subject but no tenant.
func withSubject(ctx context.Context, subject string) context.Context {
	return internaljwt.ContextWithClaims(ctx, internaljwt.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: subject},
	})
}

func withEvent(ctx context.Context, tokenUse, eventPublicID string) context.Context {
	return internaljwt.ContextWithClaims(ctx, internaljwt.Claims{
		TokenUse:      tokenUse,
		EventPublicID: eventPublicID,
	})
}

func TestSubjectFromContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ctx    context.Context
		want   string
		wantOK bool
	}{
		{
			name:   "verified subject",
			ctx:    withSubject(context.Background(), "user-1"),
			want:   "user-1",
			wantOK: true,
		},
		{
			name:   "no claims",
			ctx:    context.Background(),
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty subject claim",
			ctx:    withSubject(context.Background(), ""),
			want:   "",
			wantOK: false,
		},
		{
			name:   "blank subject claim",
			ctx:    withSubject(context.Background(), "   "),
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tenantctx.SubjectFromContext(tt.ctx)
			if ok != tt.wantOK {
				t.Errorf("SubjectFromContext() ok = %v, want %v", ok, tt.wantOK)
			}

			if got != tt.want {
				t.Errorf("SubjectFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTenantPublicIDFromContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ctx    context.Context
		want   string
		wantOK bool
	}{
		{
			name:   "verified tenant",
			ctx:    withTenant(context.Background(), "tenant-public-id"),
			want:   "tenant-public-id",
			wantOK: true,
		},
		{
			name:   "no claims",
			ctx:    context.Background(),
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty tenant claim",
			ctx:    withTenant(context.Background(), ""),
			want:   "",
			wantOK: false,
		},
		{
			name:   "blank tenant claim",
			ctx:    withTenant(context.Background(), "   "),
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tenantctx.TenantPublicIDFromContext(tt.ctx)
			if ok != tt.wantOK {
				t.Errorf("TenantPublicIDFromContext() ok = %v, want %v", ok, tt.wantOK)
			}

			if got != tt.want {
				t.Errorf("TenantPublicIDFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnsure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		ctx            context.Context
		tenantPublicID string
		wantErr        error
	}{
		{
			name:           "matches context tenant",
			ctx:            withTenant(context.Background(), "tenant-public-id"),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "differs from context tenant",
			ctx:            withTenant(context.Background(), "tenant-public-id"),
			tenantPublicID: "other-tenant-public-id",
			wantErr:        tenantctx.ErrMismatch,
		},
		{
			name:           "missing context tenant",
			ctx:            context.Background(),
			tenantPublicID: "tenant-public-id",
			wantErr:        tenantctx.ErrMissing,
		},
		{
			name:           "empty context tenant",
			ctx:            withTenant(context.Background(), ""),
			tenantPublicID: "tenant-public-id",
			wantErr:        tenantctx.ErrMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tenantctx.Ensure(tt.ctx, tt.tenantPublicID)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Ensure() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		ctx            context.Context
		tenantPublicID string
		wantErr        error
	}{
		{
			name:           "matches context tenant",
			ctx:            withTenant(context.Background(), "tenant-public-id"),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "differs from context tenant",
			ctx:            withTenant(context.Background(), "tenant-public-id"),
			tenantPublicID: "other-tenant-public-id",
			wantErr:        tenantctx.ErrMismatch,
		},
		{
			name:           "missing context tenant is unrestricted",
			ctx:            context.Background(),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
		{
			name:           "empty context tenant is unrestricted",
			ctx:            withTenant(context.Background(), ""),
			tenantPublicID: "tenant-public-id",
			wantErr:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tenantctx.VerifyOwnership(tt.ctx, tt.tenantPublicID)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("VerifyOwnership() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEventPublicIDFromContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ctx    context.Context
		want   string
		wantOK bool
	}{
		{
			name:   "verified event",
			ctx:    withEvent(context.Background(), internaljwt.TokenUseEventAccess, "event-public-id"),
			want:   "event-public-id",
			wantOK: true,
		},
		{
			name:   "no claims",
			ctx:    context.Background(),
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty event claim",
			ctx:    withEvent(context.Background(), internaljwt.TokenUseEventAccess, ""),
			want:   "",
			wantOK: false,
		},
		{
			name:   "blank event claim",
			ctx:    withEvent(context.Background(), internaljwt.TokenUseEventAccess, "   "),
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tenantctx.EventPublicIDFromContext(tt.ctx)
			if ok != tt.wantOK {
				t.Errorf("EventPublicIDFromContext() ok = %v, want %v", ok, tt.wantOK)
			}

			if got != tt.want {
				t.Errorf("EventPublicIDFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnsureEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		ctx           context.Context
		eventPublicID string
		wantErr       error
	}{
		{
			name:          "matches context event",
			ctx:           withEvent(context.Background(), internaljwt.TokenUseEventAccess, "event-public-id"),
			eventPublicID: "event-public-id",
			wantErr:       nil,
		},
		{
			name:          "differs from context event",
			ctx:           withEvent(context.Background(), internaljwt.TokenUseEventAccess, "event-public-id"),
			eventPublicID: "other-event-public-id",
			wantErr:       tenantctx.ErrEventMismatch,
		},
		{
			name:          "event access without an event claim",
			ctx:           withEvent(context.Background(), internaljwt.TokenUseEventAccess, ""),
			eventPublicID: "event-public-id",
			wantErr:       tenantctx.ErrEventMissing,
		},
		{
			name:          "blank event claim on event access",
			ctx:           withEvent(context.Background(), internaljwt.TokenUseEventAccess, "   "),
			eventPublicID: "event-public-id",
			wantErr:       tenantctx.ErrEventMissing,
		},
		{
			name:          "service token without an event claim is unrestricted",
			ctx:           withEvent(context.Background(), internaljwt.TokenUseService, ""),
			eventPublicID: "event-public-id",
			wantErr:       nil,
		},
		{
			name:          "service token carrying a differing event",
			ctx:           withEvent(context.Background(), internaljwt.TokenUseService, "event-public-id"),
			eventPublicID: "other-event-public-id",
			wantErr:       tenantctx.ErrEventMismatch,
		},
		{
			name:          "tenant access without an event claim is unrestricted",
			ctx:           withTenant(context.Background(), "tenant-public-id"),
			eventPublicID: "event-public-id",
			wantErr:       nil,
		},
		{
			name:          "no claims is unrestricted",
			ctx:           context.Background(),
			eventPublicID: "event-public-id",
			wantErr:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tenantctx.EnsureEvent(tt.ctx, tt.eventPublicID)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("EnsureEvent() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

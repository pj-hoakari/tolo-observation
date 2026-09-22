package connect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/internal-jwt-handling/jwks"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"
	"github.com/pj-hoakari/internal-jwt-handling/verifier"

	observationv1 "github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1"
	"github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1/observationv1connect"
)

const (
	testTenantPublicID = "a1b2c3d4e5f60718"
	testEventPublicID  = "0f1e2d3c4b5a6978"
)

// newTestJWKSURL serves keys from an httptest endpoint, mirroring the Service
// Gateway publishing its signing keys, and returns the URL to fetch them from.
func newTestJWKSURL(t *testing.T, keys internaljwt.JWKS) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(keys); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	return server.URL
}

// newTestVerifier builds a verifier backed by an httptest JWKS endpoint serving
// keys.
func newTestVerifier(t *testing.T, keys internaljwt.JWKS) *verifier.Verifier {
	t.Helper()

	// The cooldowns are collapsed so that a refresh on an unknown kid is never
	// held off within a test run.
	cache, err := jwks.New(jwks.Config{
		URL:             newTestJWKSURL(t, keys),
		RefreshCooldown: time.Nanosecond,
		FailureCooldown: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("create JWKS cache: %v", err)
	}

	tokenVerifier, err := verifier.New(DefaultInternalJWTIssuer, DefaultInternalJWTAudience, cache)
	if err != nil {
		t.Fatalf("create internal JWT verifier: %v", err)
	}

	return tokenVerifier
}

// newTestHandler builds a handler serving the production service routes wired
// to a verifier trusting keys.
func newTestHandler(t *testing.T, keys internaljwt.JWKS) http.Handler {
	t.Helper()

	routes, err := RoutesWithVerifier(newTestVerifier(t, keys))
	if err != nil {
		t.Fatalf("RoutesWithVerifier() error = %v", err)
	}

	mux := http.NewServeMux()
	routes(mux)

	return mux
}

func newTestHandlerForJWKSURL(t *testing.T, jwksURL string) http.Handler {
	t.Helper()

	cache, err := jwks.New(jwks.Config{
		URL:             jwksURL,
		RefreshCooldown: time.Nanosecond,
		FailureCooldown: time.Nanosecond,
		RetryBackoff:    []time.Duration{},
	})
	if err != nil {
		t.Fatalf("create JWKS cache: %v", err)
	}

	tokenVerifier, err := verifier.New(DefaultInternalJWTIssuer, DefaultInternalJWTAudience, cache)
	if err != nil {
		t.Fatalf("create internal JWT verifier: %v", err)
	}

	routes, err := RoutesWithVerifier(tokenVerifier)
	if err != nil {
		t.Fatalf("RoutesWithVerifier() error = %v", err)
	}

	mux := http.NewServeMux()
	routes(mux)

	return mux
}

// mintEventToken issues the credential the observation RPCs expect: an event
// token of this service's issuer and audience, granting scope.
func mintEventToken(t *testing.T, scope string) (string, internaljwt.JWKS) {
	t.Helper()

	return mintInternalJWTFor(t, jwtgen.Config{
		TokenUse:       internaljwt.TokenUseEventAccess,
		TenantPublicID: testTenantPublicID,
		EventPublicID:  testEventPublicID,
		Scope:          scope,
	})
}

// mintInternalJWTFor issues an internal JWT signed by a fresh key, returning
// the Authorization header value and the JWKS document publishing the key. The
// issuer and the audience fall back to the ones this service verifies against.
func mintInternalJWTFor(t *testing.T, config jwtgen.Config) (string, internaljwt.JWKS) {
	t.Helper()

	if config.Issuer == "" {
		config.Issuer = DefaultInternalJWTIssuer
	}

	if config.Audience == "" {
		config.Audience = DefaultInternalJWTAudience
	}

	config.KeyID = "test-key"
	config.TTL = time.Hour

	output, err := jwtgen.Generate(config)
	if err != nil {
		t.Fatalf("generate internal JWT: %v", err)
	}

	return "Bearer " + output.Token, output.JWKS
}

// listEdgeDevicesRequest is the call the authorization tests make. Every
// handler is still unimplemented, so CodeUnimplemented is the proof that a
// call passed authorization.
func listEdgeDevicesRequest(authorization string) *connectrpc.Request[observationv1.ListEdgeDevicesRequest] {
	req := connectrpc.NewRequest(&observationv1.ListEdgeDevicesRequest{EventId: testEventPublicID})
	if authorization != "" {
		req.Header().Set("Authorization", authorization)
	}

	return req
}

func TestRoutesWithJWTSettings(t *testing.T) {
	t.Parallel()

	t.Run("verifies a token against the JWKS the settings locate", func(t *testing.T) {
		t.Parallel()

		authorization, keys := mintEventToken(t, "events.read")

		settings := DefaultJWTSettings()
		settings.JWKSURL = newTestJWKSURL(t, keys)

		routes, err := RoutesWithJWTSettings(settings)
		if err != nil {
			t.Fatalf("RoutesWithJWTSettings() error = %v", err)
		}

		mux := http.NewServeMux()
		routes(mux)

		httpServer := httptest.NewServer(mux)
		t.Cleanup(httpServer.Close)
		client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

		_, err = client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(authorization))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnimplemented; got != want {
			t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
		}
	})

	t.Run("rejects settings without a JWKS URL", func(t *testing.T) {
		t.Parallel()

		settings := DefaultJWTSettings()
		settings.JWKSURL = ""

		_, err := RoutesWithJWTSettings(settings)
		if !errors.Is(err, jwks.ErrMissingURL) {
			t.Fatalf("RoutesWithJWTSettings() error = %v, want %v", err, jwks.ErrMissingURL)
		}
	})
}

func TestEdgeDeviceServiceAuthz(t *testing.T) {
	t.Parallel()

	authorization, keys := mintEventToken(t, "events.read")
	httpServer := httptest.NewServer(newTestHandler(t, keys))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

	t.Run("rejects missing bearer token", func(t *testing.T) {
		_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(""))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
			t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
		}
	})

	// Every handler is unimplemented, so reaching one is what the policy
	// admits: an event token granting events.read passes authorization.
	t.Run("accepts an event token with the required scope", func(t *testing.T) {
		_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(authorization))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnimplemented; got != want {
			t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
		}
	})
}

func TestEdgeDeviceServiceAuthzRejectsTenantToken(t *testing.T) {
	t.Parallel()

	// The RPC names event_access as the only token_use it accepts, so a tenant
	// token is not a credential for it even with the scope granted.
	authorization, keys := mintInternalJWTFor(t, jwtgen.Config{
		TokenUse:       internaljwt.TokenUseTenantAccess,
		TenantPublicID: testTenantPublicID,
		Scope:          "events.read",
	})
	httpServer := httptest.NewServer(newTestHandler(t, keys))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(authorization))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
		t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
	}
}

func TestEdgeDeviceServiceAuthzRejectsServiceToken(t *testing.T) {
	t.Parallel()

	authorization, keys := mintInternalJWTFor(t, jwtgen.Config{TokenUse: internaljwt.TokenUseService})
	httpServer := httptest.NewServer(newTestHandler(t, keys))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(authorization))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
		t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
	}
}

// TestMeasurementIngestServiceAuthzAcceptsServiceToken covers the one RPC whose
// policy also names service, for the Guest Service reporting QR counts.
func TestMeasurementIngestServiceAuthzAcceptsServiceToken(t *testing.T) {
	t.Parallel()

	authorization, keys := mintInternalJWTFor(t, jwtgen.Config{TokenUse: internaljwt.TokenUseService})
	httpServer := httptest.NewServer(newTestHandler(t, keys))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewMeasurementIngestServiceClient(httpServer.Client(), httpServer.URL)

	req := connectrpc.NewRequest(&observationv1.ReportMeasurementsRequest{EventId: testEventPublicID})
	req.Header().Set("Authorization", authorization)

	_, err := client.ReportMeasurements(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnimplemented; got != want {
		t.Fatalf("ReportMeasurements() error code = %v, want %v", got, want)
	}
}

func TestEdgeDeviceServiceAuthzRejectsMissingScope(t *testing.T) {
	t.Parallel()

	authorization, keys := mintEventToken(t, "events.manage")
	httpServer := httptest.NewServer(newTestHandler(t, keys))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(authorization))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
	}
}

func TestEdgeDeviceServiceAuthzRejectsUnknownSigningKey(t *testing.T) {
	t.Parallel()

	// The handler trusts a JWKS publishing neither the key nor the kid that
	// signed the token below.
	_, trustedKeys := mintEventToken(t, "events.read")
	for i := range trustedKeys.Keys {
		trustedKeys.Keys[i].KeyID = "other-key"
	}

	foreignAuthorization, _ := mintEventToken(t, "events.read")
	httpServer := httptest.NewServer(newTestHandler(t, trustedKeys))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(foreignAuthorization))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
		t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
	}
}

func TestEdgeDeviceServiceAuthzUnavailableWhenJWKSUnreachable(t *testing.T) {
	t.Parallel()

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(jwksServer.Close)

	authorization, _ := mintEventToken(t, "events.read")
	httpServer := httptest.NewServer(newTestHandlerForJWKSURL(t, jwksServer.URL))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(authorization))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnavailable; got != want {
		t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
	}
}

func TestEdgeDeviceServiceAuthzRejectsAudienceMismatch(t *testing.T) {
	t.Parallel()

	// The token names another service as its audience, so it is not a
	// credential this service may accept even though the key verifies.
	authorization, keys := mintInternalJWTFor(t, jwtgen.Config{
		Audience:       "other-service",
		TokenUse:       internaljwt.TokenUseEventAccess,
		TenantPublicID: testTenantPublicID,
		EventPublicID:  testEventPublicID,
		Scope:          "events.read",
	})
	httpServer := httptest.NewServer(newTestHandler(t, keys))
	t.Cleanup(httpServer.Close)
	client := observationv1connect.NewEdgeDeviceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := client.ListEdgeDevices(context.Background(), listEdgeDevicesRequest(authorization))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
		t.Fatalf("ListEdgeDevices() error code = %v, want %v", got, want)
	}
}

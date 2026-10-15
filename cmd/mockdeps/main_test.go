package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pj-hoakari/internal-jwt-handling/jwks"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"
	"github.com/pj-hoakari/internal-jwt-handling/verifier"
)

func TestTokenVerifiesAgainstServedJWKS(t *testing.T) {
	generator, err := jwtgen.NewGenerator("")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(newHandler(generator))
	defer server.Close()

	res, err := http.Post(server.URL+"/token", "application/json", strings.NewReader(
		`{"tenant_id":"0123456789abcdef","event_id":"fedcba9876543210","scope":"events.report","ttl":"5m"}`,
	))
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /token status = %d", res.StatusCode)
	}

	var output jwtgen.Output
	if err := json.NewDecoder(res.Body).Decode(&output); err != nil {
		t.Fatal(err)
	}

	cache, err := jwks.New(jwks.Config{URL: server.URL + "/.well-known/jwks.json"})
	if err != nil {
		t.Fatal(err)
	}

	tokenVerifier, err := verifier.New("service-gateway", "tolo-observation", cache)
	if err != nil {
		t.Fatal(err)
	}

	claims, err := tokenVerifier.Verify(t.Context(), output.Token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}

	if claims.TokenUse != "event_access" || claims.TenantPublicID != "0123456789abcdef" ||
		claims.EventPublicID != "fedcba9876543210" || claims.Scope != "events.report" {
		t.Fatalf("claims = %+v", claims)
	}

	if ttl := claims.ExpiresAt.Sub(claims.IssuedAt.Time); ttl != 5*time.Minute {
		t.Fatalf("ttl = %s, want 5m", ttl)
	}
}

func TestTokenRejectsInvalidTTL(t *testing.T) {
	if _, err := decodeTokenRequest(strings.NewReader(`{"ttl":"soon"}`)); err == nil {
		t.Fatal("want an error for an unparsable ttl")
	}
}

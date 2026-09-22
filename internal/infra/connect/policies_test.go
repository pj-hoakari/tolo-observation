package connect

import (
	"slices"
	"testing"

	"github.com/pj-hoakari/protoc-gen-authz-go/authz"

	"github.com/pj-hoakari/tolo-observation/gen/tolo/observation/v1/observationv1connect"
)

// TestGeneratedPoliciesMatchTheSpecification pins the authorization table of
// observation_spec.md to what the proto generates, procedure by procedure.
func TestGeneratedPoliciesMatchTheSpecification(t *testing.T) {
	t.Parallel()

	want := map[string]authz.Policy{
		observationv1connect.MeasurementIngestServiceReportMeasurementsProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access", "service"},
			RequiredScopes: []string{"events.report"},
		},
		observationv1connect.EdgeDeviceServiceRegisterEdgeDeviceProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.manage"},
		},
		observationv1connect.EdgeDeviceServiceUnregisterEdgeDeviceProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.manage"},
		},
		observationv1connect.EdgeDeviceServiceListEdgeDevicesProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.read"},
		},
		observationv1connect.EdgeDeviceServiceHeartbeatProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.report"},
		},
		observationv1connect.EdgeDeviceServiceUpdateObservationPointConfigProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.operate"},
		},
		observationv1connect.ManualInterventionServiceOperateGateProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.operate"},
		},
		observationv1connect.ManualInterventionServiceToggleDangerFlagProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.operate"},
		},
		observationv1connect.ManualInterventionServiceRegisterScheduleEventProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.operate"},
		},
		observationv1connect.ManualInterventionServiceReportCongestionProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.operate"},
		},
		observationv1connect.ManualInterventionServiceCorrectQueueProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.operate"},
		},
		observationv1connect.StatusQueryServiceGetEventOverviewProcedure: {
			Level:          authz.LevelAuthenticated,
			TokenUses:      []string{"event_access"},
			RequiredScopes: []string{"events.read"},
		},
	}

	got, err := authz.Merge(
		observationv1connect.MeasurementIngestServicePolicies,
		observationv1connect.EdgeDeviceServicePolicies,
		observationv1connect.ManualInterventionServicePolicies,
		observationv1connect.StatusQueryServicePolicies,
	)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	for procedure, wantPolicy := range want {
		policy, ok := got.Lookup(procedure)
		if !ok {
			t.Errorf("%s is not named by the generated policies", procedure)

			continue
		}

		if policy.Level != wantPolicy.Level {
			t.Errorf("%s level = %v, want %v", procedure, policy.Level, wantPolicy.Level)
		}

		if !slices.Equal(policy.TokenUses, wantPolicy.TokenUses) {
			t.Errorf("%s token uses = %v, want %v", procedure, policy.TokenUses, wantPolicy.TokenUses)
		}

		if !slices.Equal(policy.RequiredScopes, wantPolicy.RequiredScopes) {
			t.Errorf("%s required scopes = %v, want %v", procedure, policy.RequiredScopes, wantPolicy.RequiredScopes)
		}
	}

	// A procedure served without an expectation here would be authorized by a
	// rule nothing in this repository states.
	for procedure := range got {
		if _, ok := want[procedure]; !ok {
			t.Errorf("%s is served without a declared policy", procedure)
		}
	}
}

package v1alpha1

import (
	"testing"

	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
)

func TestFerrVaultConnection_ResolvedModeDefaultsToFerrVault(t *testing.T) {
	conn := FerrVaultConnection{
		Spec: ffv1alpha1.FerrFlowConnectionSpec{Mode: ""},
	}
	if got := conn.ResolvedMode(); got != ffv1alpha1.ModeFerrVault {
		t.Fatalf("ResolvedMode() = %q, want %q for an empty mode", got, ffv1alpha1.ModeFerrVault)
	}
}

func TestFerrVaultConnection_ResolvedModeHonoursExplicitFerrFlow(t *testing.T) {
	conn := FerrVaultConnection{
		Spec: ffv1alpha1.FerrFlowConnectionSpec{Mode: ffv1alpha1.ModeFerrFlow},
	}
	if got := conn.ResolvedMode(); got != ffv1alpha1.ModeFerrFlow {
		t.Fatalf("ResolvedMode() = %q, want %q when explicitly set", got, ffv1alpha1.ModeFerrFlow)
	}
}

package v1alpha1

import (
	"testing"
)

func TestFerrVaultConnection_ResolvedModeDefaultsToFerrVault(t *testing.T) {
	conn := FerrVaultConnection{
		Spec: ConnectionSpec{Mode: ""},
	}
	if got := conn.ResolvedMode(); got != ModeFerrVault {
		t.Fatalf("ResolvedMode() = %q, want %q for an empty mode", got, ModeFerrVault)
	}
}

func TestFerrVaultConnection_ResolvedModeHonoursExplicitCloud(t *testing.T) {
	conn := FerrVaultConnection{
		Spec: ConnectionSpec{Mode: ModeCloud},
	}
	if got := conn.ResolvedMode(); got != ModeCloud {
		t.Fatalf("ResolvedMode() = %q, want %q when explicitly set", got, ModeCloud)
	}
}

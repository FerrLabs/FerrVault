package main

import (
	"testing"
	"time"
)

func TestReconcileTimeoutCannotDisableTheGuardrail(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Minute, 5 * time.Second} {
		if err := validateReconcileTimeout(d); err == nil {
			t.Errorf("--reconcile-timeout=%s was accepted", d)
		}
	}
}

func TestTheDefaultReconcileTimeoutIsAccepted(t *testing.T) {
	if err := validateReconcileTimeout(2 * time.Minute); err != nil {
		t.Fatal(err)
	}
}

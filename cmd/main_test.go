package main

import (
	corev1 "k8s.io/api/core/v1"
	"testing"
	"time"
)

func TestReconcileTimeoutCannotDisableTheGuardrail(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Minute, 5 * time.Second, 20 * time.Second} {
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

func TestSecretsAreNeverServedFromTheTypedCache(t *testing.T) {
	for _, obj := range uncachedReadKinds() {
		if _, ok := obj.(*corev1.Secret); ok {
			return
		}
	}
	t.Fatal("Secret reads go through the cache, which holds every Secret in the cluster in memory")
}

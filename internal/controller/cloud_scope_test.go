package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
	"github.com/FerrLabs/FerrVault/internal/ferrvault"
)

type countingVault struct {
	fakeVault
	bulk *int
}

func (c countingVault) BulkReveal(ctx context.Context, org, project, vault, ns string, names []string) (*ferrvault.BulkRevealResponse, error) {
	*c.bulk++
	return c.fakeVault.BulkReveal(ctx, org, project, vault, ns, names)
}

func reconcileWithScope(t *testing.T, mode, organization, project string) (string, int) {
	t.Helper()
	t.Cleanup(func() {
		SyncDuration.Reset()
		SyncErrors.Reset()
		LastSyncTimestamp.Reset()
		RefreshInterval.Reset()
	})
	f := newReconcileFixture(t, map[string]string{"API_KEY": "v"}, interceptor.Funcs{})
	ctx := context.Background()

	var conn fvv1alpha1.FerrVaultConnection
	if err := f.client.Get(ctx, types.NamespacedName{Namespace: "default", Name: "conn"}, &conn); err != nil {
		t.Fatal(err)
	}
	conn.Spec.Mode = mode
	conn.Spec.Organization = organization
	if err := f.client.Update(ctx, &conn); err != nil {
		t.Fatal(err)
	}
	var cr fvv1alpha1.FerrVaultSecret
	if err := f.client.Get(ctx, f.req.NamespacedName, &cr); err != nil {
		t.Fatal(err)
	}
	cr.Spec.Project = project
	cr.Spec.RolloutRestart = nil
	if err := f.client.Update(ctx, &cr); err != nil {
		t.Fatal(err)
	}

	bulk := 0
	f.r.ClientFactory = func(string, string) (ferrvaultClient, error) {
		return countingVault{fakeVault: fakeVault{secrets: map[string]string{"API_KEY": "v"}}, bulk: &bulk}, nil
	}
	if _, err := f.r.Reconcile(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Get(ctx, f.req.NamespacedName, &cr); err != nil {
		t.Fatal(err)
	}
	ready := meta.FindStatusCondition(cr.Status.Conditions, "Ready")
	if ready == nil {
		t.Fatal("no Ready condition after reconcile")
	}
	return ready.Reason, bulk
}

func TestCloudModeWithoutProjectFailsBeforeCallingTheAPI(t *testing.T) {
	reason, calls := reconcileWithScope(t, fvv1alpha1.ModeCloud, "acme", "")
	if reason != "MissingCloudScope" || calls != 0 {
		t.Fatalf("reason=%q bulk calls=%d, want MissingCloudScope and no call", reason, calls)
	}
}

func TestCloudModeWithoutOrganizationFailsBeforeCallingTheAPI(t *testing.T) {
	reason, calls := reconcileWithScope(t, fvv1alpha1.ModeCloud, "", "billing")
	if reason != "MissingCloudScope" || calls != 0 {
		t.Fatalf("reason=%q bulk calls=%d, want MissingCloudScope and no call", reason, calls)
	}
}

func TestCloudModeWithFullScopeReveals(t *testing.T) {
	reason, calls := reconcileWithScope(t, fvv1alpha1.ModeCloud, "acme", "billing")
	if reason == "MissingCloudScope" || calls != 1 {
		t.Fatalf("reason=%q bulk calls=%d, want one reveal", reason, calls)
	}
}

func TestFerrVaultModeSyncsWithoutProjectOrOrganization(t *testing.T) {
	reason, calls := reconcileWithScope(t, fvv1alpha1.ModeFerrVault, "", "")
	if reason == "MissingCloudScope" || calls != 0 {
		t.Fatalf("reason=%q bulk calls=%d, want a ferrvault-mode sync", reason, calls)
	}
}

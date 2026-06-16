package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

func (r *FerrVaultSecretReconciler) secretsReferencingConnection(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	var list fvv1alpha1.FerrVaultSecretList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{fvSecretConnectionRefIndexKey: obj.GetName()},
	); err != nil {
		return nil
	}
	return fvRequestsForList(list.Items)
}

func (r *FerrVaultSecretReconciler) secretsReferencingTokenSecret(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	var conns fvv1alpha1.FerrVaultConnectionList
	if err := r.List(ctx, &conns,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{".spec.tokenSecretRef.name": obj.GetName()},
	); err != nil {
		return nil
	}
	if len(conns.Items) == 0 {
		return nil
	}
	var all []reconcile.Request
	for i := range conns.Items {
		var list fvv1alpha1.FerrVaultSecretList
		if err := r.List(ctx, &list,
			client.InNamespace(conns.Items[i].Namespace),
			client.MatchingFields{fvSecretConnectionRefIndexKey: conns.Items[i].Name},
		); err != nil {
			continue
		}
		all = append(all, fvRequestsForList(list.Items)...)
	}
	return all
}

func fvRequestsForList(items []fvv1alpha1.FerrVaultSecret) []reconcile.Request {
	reqs := make([]reconcile.Request, 0, len(items))
	for i := range items {
		reqs = append(reqs, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: items[i].Namespace,
				Name:      items[i].Name,
			},
		})
	}
	return reqs
}

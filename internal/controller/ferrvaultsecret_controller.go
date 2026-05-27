package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fvv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/ferrvault/v1alpha1"
	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrLabs/FerrFlow-Operator/internal/ferrflow"
)

const (
	fvSecretConnectionRefIndexKey = ".spec.connectionRef.name"
	fvAnnotationContentHash       = "ferrvault.com/content-hash"
	fvAnnotationRestartedAt       = "ferrvault.com/restarted-at"
	fvSecretFinalizer             = "ferrvault.com/secret-cleanup"
)

type FerrVaultSecretReconciler struct {
	client.Client
	Scheme                 *runtime.Scheme
	DefaultRefreshInterval time.Duration
	ClientFactory          ClientFactory
	Broker                 *TokenBroker
}

// +kubebuilder:rbac:groups=ferrvault.com,resources=ferrvaultsecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ferrvault.com,resources=ferrvaultsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ferrvault.com,resources=ferrvaultsecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups=ferrvault.com,resources=ferrvaultconnections,verbs=get;list;watch

func (r *FerrVaultSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("ferrvaultsecret", req.NamespacedName)

	begin := time.Now()
	result := "failure"
	defer func() {
		ObserveReconcile(begin, result)
	}()

	var cr fvv1alpha1.FerrVaultSecret
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			DeleteLastSyncTimestamp(req.Namespace, req.Name)
			result = "success"
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("load FerrVaultSecret: %w", err)
	}

	if cr.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(&cr, fvSecretFinalizer) {
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
			}
			result = "success"
			return ctrl.Result{}, nil
		}
	} else {
		logger.Info("running pre-delete cleanup")
		DeleteLastSyncTimestamp(cr.Namespace, cr.Name)
		if controllerutil.RemoveFinalizer(&cr, fvSecretFinalizer) {
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
			}
		}
		result = "success"
		return ctrl.Result{}, nil
	}

	var conn fvv1alpha1.FerrVaultConnection
	connKey := types.NamespacedName{Namespace: req.Namespace, Name: cr.Spec.ConnectionRef.Name}
	if err := r.Get(ctx, connKey, &conn); err != nil {
		return r.failReady(ctx, &cr, "ConnectionNotFound", fmt.Sprintf(
			"FerrVaultConnection %q: %v", cr.Spec.ConnectionRef.Name, err))
	}

	token, err := r.loadToken(ctx, &conn)
	if err != nil {
		return r.failReady(ctx, &cr, "TokenUnreadable", err.Error())
	}

	factory := r.ClientFactory
	if factory == nil {
		factory = defaultClientFactory
	}
	ffc, err := factory(conn.Spec.URL, token)
	if err != nil {
		return r.failReady(ctx, &cr, "InvalidConnection", err.Error())
	}

	var reveal *ferrflow.BulkRevealResponse
	switch conn.Spec.ResolvedMode() {
	case ffv1alpha1.ModeFerrVault:
		reveal, err = ffc.RevealFromVault(ctx, cr.Spec.Vault, cr.Spec.Selector.Names)
	default:
		reveal, err = ffc.BulkReveal(
			ctx,
			conn.Spec.Organization,
			cr.Spec.Project,
			cr.Spec.Vault,
			cr.Namespace,
			cr.Spec.Selector.Names,
		)
	}
	if err != nil {
		if ferrflow.IsAuthError(err) {
			return r.failReadyWithRequeue(ctx, &cr, "AuthFailed", err.Error(), 5*time.Minute)
		}
		if ferrflow.IsNotFound(err) {
			return r.failReadyWithRequeue(ctx, &cr, "VaultNotFound", err.Error(), r.refreshInterval(&cr))
		}
		return r.failReadyWithRequeue(ctx, &cr, "Unreachable", err.Error(), r.refreshInterval(&cr))
	}

	transformed, err := ApplyTransforms(reveal.Secrets, cr.Spec.Transforms)
	if err != nil {
		return r.failReady(ctx, &cr, "TransformError", err.Error())
	}

	newHash := hashSecretData(transformed)
	secret, oldHash, err := r.ensureTargetSecret(ctx, &cr, transformed, newHash)
	if err != nil {
		return r.failReady(ctx, &cr, "SecretWriteFailed", err.Error())
	}
	contentChanged := oldHash != "" && oldHash != newHash
	logger.Info("synced secret",
		"target", secret.Name,
		"keys", len(transformed),
		"missing", len(reveal.Missing),
		"transforms", len(cr.Spec.Transforms),
		"contentChanged", contentChanged,
	)

	if contentChanged && len(cr.Spec.RolloutRestart) > 0 {
		if err := r.triggerRollouts(ctx, &cr); err != nil {
			logger.Error(err, "rollout restart failed")
		}
	}

	syncedKeys := make([]string, 0, len(transformed))
	for k := range transformed {
		syncedKeys = append(syncedKeys, k)
	}
	sort.Strings(syncedKeys)
	sort.Strings(reveal.Missing)

	now := metav1.Now()
	cr.Status.LastSyncedAt = &now
	cr.Status.SyncedKeys = syncedKeys
	cr.Status.MissingKeys = reveal.Missing
	cr.Status.ObservedGeneration = cr.Generation

	readyStatus := metav1.ConditionTrue
	readyReason := "Synced"
	readyMessage := fmt.Sprintf("%d key(s) synced into %s", len(syncedKeys), secret.Name)
	if len(reveal.Missing) > 0 {
		readyStatus = metav1.ConditionFalse
		readyReason = "MissingKeys"
		readyMessage = fmt.Sprintf("missing in FerrVault: %v", reveal.Missing)
	}
	setCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  readyStatus,
		Reason:  readyReason,
		Message: readyMessage,
	})

	if err := r.Status().Update(ctx, &cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}

	if len(reveal.Missing) > 0 {
		IncSyncError("MissingKeys")
	} else {
		SetLastSyncTimestamp(cr.Namespace, cr.Name)
		result = "success"
	}

	return ctrl.Result{RequeueAfter: r.refreshInterval(&cr)}, nil
}

func (r *FerrVaultSecretReconciler) loadToken(ctx context.Context, conn *fvv1alpha1.FerrVaultConnection) (string, error) {
	broker := r.Broker
	if broker == nil {
		broker = NewTokenBroker(r.Client)
	}
	adapter := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: conn.ObjectMeta,
		Spec:       conn.Spec,
	}
	return broker.TokenFor(ctx, adapter)
}

func (r *FerrVaultSecretReconciler) ensureTargetSecret(
	ctx context.Context,
	cr *fvv1alpha1.FerrVaultSecret,
	data map[string]string,
	newHash string,
) (*corev1.Secret, string, error) {
	name := cr.Spec.Target.Name
	if name == "" {
		name = cr.Name
	}
	secretType := corev1.SecretType(cr.Spec.Target.Type)
	if secretType == "" {
		secretType = corev1.SecretTypeOpaque
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
		},
	}
	var oldHash string
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if existing, ok := secret.Annotations[fvAnnotationContentHash]; ok {
			oldHash = existing
		}
		if err := controllerutil.SetControllerReference(cr, secret, r.Scheme); err != nil {
			return err
		}
		secret.Type = secretType
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations["ferrvault.com/managed-by"] = "ferrflow-operator"
		secret.Annotations[fvAnnotationContentHash] = newHash
		secret.StringData = data
		secret.Data = nil
		return nil
	}); err != nil {
		return nil, "", err
	}
	return secret, oldHash, nil
}

func (r *FerrVaultSecretReconciler) triggerRollouts(
	ctx context.Context,
	cr *fvv1alpha1.FerrVaultSecret,
) error {
	logger := log.FromContext(ctx).WithValues("ferrvaultsecret", cr.Name)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	patchPodTemplate := func(obj client.Object, tmpl *corev1.PodTemplateSpec) error {
		if tmpl.Annotations == nil {
			tmpl.Annotations = map[string]string{}
		}
		tmpl.Annotations[fvAnnotationRestartedAt] = now
		return r.Update(ctx, obj)
	}

	var firstErr error
	for _, w := range cr.Spec.RolloutRestart {
		key := types.NamespacedName{Namespace: cr.Namespace, Name: w.Name}
		log := logger.WithValues("workload", fmt.Sprintf("%s/%s", w.Kind, w.Name))

		var err error
		switch w.Kind {
		case "Deployment":
			var d appsv1.Deployment
			if err = r.Get(ctx, key, &d); err == nil {
				err = patchPodTemplate(&d, &d.Spec.Template)
			}
		case "StatefulSet":
			var s appsv1.StatefulSet
			if err = r.Get(ctx, key, &s); err == nil {
				err = patchPodTemplate(&s, &s.Spec.Template)
			}
		case "DaemonSet":
			var ds appsv1.DaemonSet
			if err = r.Get(ctx, key, &ds); err == nil {
				err = patchPodTemplate(&ds, &ds.Spec.Template)
			}
		default:
			err = fmt.Errorf("unsupported Kind %q", w.Kind)
		}

		if err != nil {
			log.Error(err, "rollout patch failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		log.Info("rollout triggered")
	}
	return firstErr
}

func (r *FerrVaultSecretReconciler) refreshInterval(cr *fvv1alpha1.FerrVaultSecret) time.Duration {
	if cr.Spec.RefreshInterval == "" {
		return r.DefaultRefreshInterval
	}
	d, err := time.ParseDuration(cr.Spec.RefreshInterval)
	if err != nil {
		return r.DefaultRefreshInterval
	}
	return d
}

func (r *FerrVaultSecretReconciler) failReady(
	ctx context.Context,
	cr *fvv1alpha1.FerrVaultSecret,
	reason, message string,
) (ctrl.Result, error) {
	return r.failReadyWithRequeue(ctx, cr, reason, message, r.refreshInterval(cr))
}

func (r *FerrVaultSecretReconciler) failReadyWithRequeue(
	ctx context.Context,
	cr *fvv1alpha1.FerrVaultSecret,
	reason, message string,
	after time.Duration,
) (ctrl.Result, error) {
	IncSyncError(reason)
	setCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	cr.Status.ObservedGeneration = cr.Generation
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status with %s: %w", reason, err)
	}
	return ctrl.Result{RequeueAfter: after}, nil
}

func (r *FerrVaultSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&fvv1alpha1.FerrVaultSecret{},
		fvSecretConnectionRefIndexKey,
		func(obj client.Object) []string {
			s := obj.(*fvv1alpha1.FerrVaultSecret)
			return []string{s.Spec.ConnectionRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&fvv1alpha1.FerrVaultSecret{}).
		Owns(&corev1.Secret{}).
		Watches(
			&fvv1alpha1.FerrVaultConnection{},
			handler.EnqueueRequestsFromMapFunc(r.secretsReferencingConnection),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.secretsReferencingTokenSecret),
		).
		Named("ferrvaultsecret").
		Complete(r)
}

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

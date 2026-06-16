package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

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

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
	"github.com/FerrLabs/FerrVault/internal/ferrvault"
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

	var reveal *ferrvault.BulkRevealResponse
	switch conn.ResolvedMode() {
	case fvv1alpha1.ModeFerrVault:
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
		if ferrvault.IsAuthError(err) {
			return r.failReadyWithRequeue(ctx, &cr, "AuthFailed", err.Error(), 5*time.Minute)
		}
		if ferrvault.IsNotFound(err) {
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

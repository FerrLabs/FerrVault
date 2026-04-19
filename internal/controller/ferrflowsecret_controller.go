// Package controller holds the reconciliation loops for the operator's CRDs.
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	ffv1alpha1 "github.com/FerrFlow-Org/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrFlow-Org/FerrFlow-Operator/internal/ferrflow"
)

// connectionRefIndexKey is the field-indexer key for
// `ffv1alpha1.FerrFlowSecret.spec.connectionRef.name`. Defined here rather
// than inline so the watch setup and the list call in the map func stay in
// sync.
const connectionRefIndexKey = ".spec.connectionRef.name"

const (
	// annotationContentHash is a digest of the synced key/value pairs, used to
	// detect content drift across reconciles — on change, rolloutRestart
	// workloads get their pod template annotated to trigger a rolling update.
	annotationContentHash = "ferrflow.io/content-hash"

	// annotationRestartedAt is the field patched on a workload's pod template
	// when a secret rotation is detected. Same mechanism `kubectl rollout
	// restart` uses internally — writing any new value to a pod template
	// annotation triggers the controller's rollout.
	annotationRestartedAt = "ferrflow.io/restarted-at"
)

// FerrFlowSecretReconciler reconciles a FerrFlowSecret object against its
// upstream source (a vault in the FerrFlow API) and a downstream sink (a
// Kubernetes Secret in the same namespace).
type FerrFlowSecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// DefaultRefreshInterval is used when the spec leaves `refreshInterval`
	// blank or set to an unparseable value. Set by cmd/main.go at startup.
	DefaultRefreshInterval time.Duration

	// ClientFactory builds the FerrFlow HTTP client. Left nil in production —
	// the reconciler falls back to defaultClientFactory, which hands back a
	// real *ferrflow.Client. Unit tests inject a fake here.
	ClientFactory ClientFactory
}

// +kubebuilder:rbac:groups=ferrflow.io,resources=ferrflowsecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ferrflow.io,resources=ferrflowsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ferrflow.io,resources=ferrflowsecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups=ferrflow.io,resources=ferrflowconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;patch

// Reconcile is the entrypoint called by controller-runtime whenever an
// event (create/update) fires on a `FerrFlowSecret` or on any object the
// controller explicitly watches (the referenced Secret and Connection).
func (r *FerrFlowSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("ferrflowsecret", req.NamespacedName)

	// Metrics: the result label starts as "failure" and gets flipped to
	// "success" only on the happy-path return. Every early return (including
	// failReady*) therefore records a failure observation automatically.
	begin := time.Now()
	result := "failure"
	defer func() {
		ObserveReconcile(begin, result)
	}()

	// --- 1. Load the CR.
	var cr ffv1alpha1.FerrFlowSecret
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			// Deleted — owner references on the generated Secret will GC it.
			// Drop the per-CR gauge so label cardinality doesn't grow forever.
			DeleteLastSyncTimestamp(req.Namespace, req.Name)
			result = "success"
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("load FerrFlowSecret: %w", err)
	}

	// --- 2. Resolve the connection and build a client.
	var conn ffv1alpha1.FerrFlowConnection
	connKey := types.NamespacedName{Namespace: req.Namespace, Name: cr.Spec.ConnectionRef.Name}
	if err := r.Get(ctx, connKey, &conn); err != nil {
		return r.failReady(ctx, &cr, "ConnectionNotFound", fmt.Sprintf(
			"FerrFlowConnection %q: %v", cr.Spec.ConnectionRef.Name, err))
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

	// --- 3. Fetch the secrets from FerrFlow.
	//
	// The CR's own namespace is what the FerrFlow API uses to authorize
	// cluster-identity callers (via `cluster_authorizations.namespace_name`).
	// User-token callers ignore the header — safe to always send it.
	reveal, err := ffc.BulkReveal(
		ctx,
		conn.Spec.Organization,
		cr.Spec.Project,
		cr.Spec.Vault,
		cr.Namespace,
		cr.Spec.Selector.Names,
	)
	if err != nil {
		// 401/403 are terminal until the user fixes their token — longer backoff.
		if ferrflow.IsAuthError(err) {
			return r.failReadyWithRequeue(ctx, &cr, "AuthFailed", err.Error(), 5*time.Minute)
		}
		if ferrflow.IsNotFound(err) {
			return r.failReadyWithRequeue(ctx, &cr, "VaultNotFound", err.Error(), r.refreshInterval(&cr))
		}
		// Transport or 5xx: stamp status so the CR surfaces the failure, then
		// requeue at the normal cadence. Returning the raw error would make
		// controller-runtime retry in a tight loop without ever updating the
		// CR's Ready condition — observable as "Reconciler error" spam with no
		// user-visible signal.
		return r.failReadyWithRequeue(ctx, &cr, "Unreachable", err.Error(), r.refreshInterval(&cr))
	}

	// --- 4. Materialise the target Secret.
	newHash := hashSecretData(reveal.Secrets)
	secret, oldHash, err := r.ensureTargetSecret(ctx, &cr, reveal.Secrets, newHash)
	if err != nil {
		return r.failReady(ctx, &cr, "SecretWriteFailed", err.Error())
	}
	contentChanged := oldHash != "" && oldHash != newHash
	logger.Info("synced secret",
		"target", secret.Name,
		"keys", len(reveal.Secrets),
		"missing", len(reveal.Missing),
		"contentChanged", contentChanged,
	)

	// --- 4b. Trigger rollout restarts when content changed.
	//
	// First reconcile (oldHash == "") never rolls — we don't want creating a
	// CR that references an already-deployed workload to cause a surprise
	// restart. Only subsequent reveals that actually flip a value do.
	if contentChanged && len(cr.Spec.RolloutRestart) > 0 {
		if err := r.triggerRollouts(ctx, &cr); err != nil {
			// Don't fail the whole reconcile — the Secret is already updated;
			// surface it in the Ready condition but keep going.
			logger.Error(err, "rollout restart failed")
		}
	}

	// --- 5. Update status.
	syncedKeys := make([]string, 0, len(reveal.Secrets))
	for k := range reveal.Secrets {
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
		readyMessage = fmt.Sprintf("missing in FerrFlow: %v", reveal.Missing)
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

	// MissingKeys leaves the CR in Ready=False but the sync itself completed,
	// so count it as a failure with that reason. Everything else is a clean
	// success — stamp the timestamp gauge and flip the result label.
	if len(reveal.Missing) > 0 {
		IncSyncError("MissingKeys")
	} else {
		SetLastSyncTimestamp(cr.Namespace, cr.Name)
		result = "success"
	}

	return ctrl.Result{RequeueAfter: r.refreshInterval(&cr)}, nil
}

// loadToken reads the API token out of the referenced Secret.
func (r *FerrFlowSecretReconciler) loadToken(ctx context.Context, conn *ffv1alpha1.FerrFlowConnection) (string, error) {
	var tokenSecret corev1.Secret
	key := types.NamespacedName{
		Namespace: conn.Namespace,
		Name:      conn.Spec.TokenSecretRef.Name,
	}
	if err := r.Get(ctx, key, &tokenSecret); err != nil {
		return "", fmt.Errorf("load token Secret %s: %w", key, err)
	}
	raw, ok := tokenSecret.Data[conn.Spec.TokenSecretRef.Key]
	if !ok {
		return "", fmt.Errorf("key %q missing from token Secret %s",
			conn.Spec.TokenSecretRef.Key, key)
	}
	token := string(raw)
	if token == "" {
		return "", fmt.Errorf("token Secret %s has empty value at key %q",
			key, conn.Spec.TokenSecretRef.Key)
	}
	return token, nil
}

// ensureTargetSecret creates or updates the Kubernetes Secret that mirrors the
// revealed FerrFlow secrets. Uses an owner reference so deleting the CR GCs
// the Secret. Returns the materialised Secret plus the `content-hash`
// annotation that was on it *before* this reconcile — the caller compares
// against the new hash to decide whether rollout-restart is warranted.
func (r *FerrFlowSecretReconciler) ensureTargetSecret(
	ctx context.Context,
	cr *ffv1alpha1.FerrFlowSecret,
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
		// The CreateOrUpdate helper populates `secret` with the on-cluster
		// state before invoking the closure, so reading the annotation here
		// gets us the pre-update hash. New Secrets have nil annotations.
		if existing, ok := secret.Annotations[annotationContentHash]; ok {
			oldHash = existing
		}
		// Owner reference: deleting the CR cleans up the Secret via GC.
		if err := controllerutil.SetControllerReference(cr, secret, r.Scheme); err != nil {
			return err
		}
		secret.Type = secretType
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations["ferrflow.io/managed-by"] = "ferrflow-operator"
		secret.Annotations[annotationContentHash] = newHash
		// Replace StringData wholesale — we want drift correction, not merge.
		secret.StringData = data
		// Clear the byte map so removed keys don't linger.
		secret.Data = nil
		return nil
	}); err != nil {
		return nil, "", err
	}
	return secret, oldHash, nil
}

// hashSecretData returns a stable SHA-256 over the sorted key=value pairs so
// equal maps produce equal hashes regardless of Go's map iteration order.
// Values are NOT logged or otherwise surfaced — only the digest leaves the
// function.
func hashSecretData(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0x00})
		h.Write([]byte(data[k]))
		h.Write([]byte{0x00})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// triggerRollouts patches each workload listed in `spec.rolloutRestart` so
// its pod template re-hashes — the workload controller (Deployment /
// StatefulSet / DaemonSet) sees the change and rolls pods per its configured
// strategy. Matches what `kubectl rollout restart` does.
//
// Errors on individual workloads are logged but don't abort the whole batch:
// one missing Deployment shouldn't block restarts of the others.
func (r *FerrFlowSecretReconciler) triggerRollouts(
	ctx context.Context,
	cr *ffv1alpha1.FerrFlowSecret,
) error {
	logger := log.FromContext(ctx).WithValues("ferrflowsecret", cr.Name)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	patchPodTemplate := func(obj client.Object, tmpl *corev1.PodTemplateSpec) error {
		if tmpl.Annotations == nil {
			tmpl.Annotations = map[string]string{}
		}
		tmpl.Annotations[annotationRestartedAt] = now
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

// refreshInterval parses spec.refreshInterval with fallback to the controller
// default. Returns 0 when the user explicitly set "0s" — disabled refresh.
func (r *FerrFlowSecretReconciler) refreshInterval(cr *ffv1alpha1.FerrFlowSecret) time.Duration {
	if cr.Spec.RefreshInterval == "" {
		return r.DefaultRefreshInterval
	}
	d, err := time.ParseDuration(cr.Spec.RefreshInterval)
	if err != nil {
		return r.DefaultRefreshInterval
	}
	return d
}

// failReady stamps a Ready=False condition with the given reason/message and
// requeues after the normal refresh interval.
func (r *FerrFlowSecretReconciler) failReady(
	ctx context.Context,
	cr *ffv1alpha1.FerrFlowSecret,
	reason, message string,
) (ctrl.Result, error) {
	return r.failReadyWithRequeue(ctx, cr, reason, message, r.refreshInterval(cr))
}

func (r *FerrFlowSecretReconciler) failReadyWithRequeue(
	ctx context.Context,
	cr *ffv1alpha1.FerrFlowSecret,
	reason, message string,
	after time.Duration,
) (ctrl.Result, error) {
	// Single choke-point for failure metrics — every caller funnels through
	// here, so the counter stays in lockstep with the condition reason.
	IncSyncError(reason)
	setCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	cr.Status.ObservedGeneration = cr.Generation
	if err := r.Status().Update(ctx, cr); err != nil {
		// Status write failures bubble up — controller-runtime will retry.
		return ctrl.Result{}, fmt.Errorf("update status with %s: %w", reason, err)
	}
	return ctrl.Result{RequeueAfter: after}, nil
}

// setCondition is the minimal upsert we need: replace the entry with the same
// Type or append. Matches the semantics of `meta.SetStatusCondition` but
// avoids pulling in the helper for a single call site.
func setCondition(conds *[]metav1.Condition, c metav1.Condition) {
	c.LastTransitionTime = metav1.Now()
	for i := range *conds {
		if (*conds)[i].Type == c.Type {
			if (*conds)[i].Status == c.Status {
				// Only update the fields that changed; keep the original
				// transition time so observers can distinguish "still failing"
				// from "just started failing".
				c.LastTransitionTime = (*conds)[i].LastTransitionTime
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

// SetupWithManager wires the reconciler into the controller manager and sets
// up the watches it needs.
//
// Beyond the trivial `For` / `Owns` pair, we watch:
//
//   - `FerrFlowConnection` — a connection update (spec change or status flip)
//     is effectively a cache-invalidation event for every FerrFlowSecret
//     referencing it. Without this watch, the downstream CRs only re-reconcile
//     on their `refreshInterval` (1h by default).
//   - `Secret` — specifically the token Secret that a referenced Connection
//     points at. Rotating the token otherwise takes up to `refreshInterval`
//     to flow through, which is too long for emergency rotation.
//
// Both map funcs are bounded: each event triggers O(N) CR lookups where N is
// the number of CRs in the namespace, not cluster-wide. Field indexers keep
// those lookups cheap.
func (r *FerrFlowSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Index FerrFlowSecret by the name of the Connection it references.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&ffv1alpha1.FerrFlowSecret{},
		connectionRefIndexKey,
		func(obj client.Object) []string {
			s := obj.(*ffv1alpha1.FerrFlowSecret)
			return []string{s.Spec.ConnectionRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&ffv1alpha1.FerrFlowSecret{}).
		Owns(&corev1.Secret{}).
		Watches(
			&ffv1alpha1.FerrFlowConnection{},
			handler.EnqueueRequestsFromMapFunc(r.secretsReferencingConnection),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.secretsReferencingTokenSecret),
		).
		Named("ferrflowsecret").
		Complete(r)
}

// secretsReferencingConnection returns reconcile requests for every
// FerrFlowSecret in the same namespace that references the given Connection
// by name. Used to invalidate the derived Secret when the Connection spec
// changes (URL flip, org change) or when the Connection's own Ready
// condition flips (which the Connection reconciler writes after a token
// Secret change, so this is one of the two paths that propagates token
// rotations).
func (r *FerrFlowSecretReconciler) secretsReferencingConnection(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	var list ffv1alpha1.FerrFlowSecretList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{connectionRefIndexKey: obj.GetName()},
	); err != nil {
		return nil
	}
	return requestsForList(list.Items)
}

// secretsReferencingTokenSecret handles the direct-path case: a Secret is
// updated, find every FerrFlowConnection in the namespace whose
// `tokenSecretRef.name` matches, then enqueue each FerrFlowSecret that
// references any of those Connections.
//
// This covers the case where the Connection reconciler is somehow delayed
// (e.g. backoff, controller restart) — we don't need to wait for it to fire
// before the FerrFlowSecret reconciles against the new token.
func (r *FerrFlowSecretReconciler) secretsReferencingTokenSecret(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	// The Connection reconciler already registered an index on
	// `.spec.tokenSecretRef.name`, reuse it.
	var conns ffv1alpha1.FerrFlowConnectionList
	if err := r.List(ctx, &conns,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{".spec.tokenSecretRef.name": obj.GetName()},
	); err != nil {
		return nil
	}
	if len(conns.Items) == 0 {
		// Not a token Secret that any Connection references. Common case for
		// every other Secret in the namespace — bail cheap.
		return nil
	}
	var all []reconcile.Request
	for i := range conns.Items {
		var list ffv1alpha1.FerrFlowSecretList
		if err := r.List(ctx, &list,
			client.InNamespace(conns.Items[i].Namespace),
			client.MatchingFields{connectionRefIndexKey: conns.Items[i].Name},
		); err != nil {
			continue
		}
		all = append(all, requestsForList(list.Items)...)
	}
	return all
}

// requestsForList turns a slice of CRs into reconcile requests. Same pattern
// used by the Connection reconciler — keep the two in sync.
func requestsForList(items []ffv1alpha1.FerrFlowSecret) []reconcile.Request {
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

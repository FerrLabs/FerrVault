package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

func (r *FerrVaultSecretReconciler) loadToken(ctx context.Context, conn *fvv1alpha1.FerrVaultConnection) (string, error) {
	broker := r.Broker
	if broker == nil {
		broker = NewTokenBroker(r.Client)
	}
	return broker.TokenFor(ctx, conn)
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
		secret.Annotations["ferrvault.com/managed-by"] = "ferrvault-operator"
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

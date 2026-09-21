// Command manager is the entry point for the ferrvault-operator binary.
//
// It stands up a controller-runtime Manager, registers the CRD types in the
// scheme, wires the FerrVaultSecret reconciler in, and blocks on the
// manager's run loop until the process receives SIGTERM / SIGINT.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
	"github.com/FerrLabs/FerrVault/internal/controller"
	"github.com/FerrLabs/FerrVault/internal/ferrvault"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fvv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr            string
		probeAddr              string
		enableLeaderElection   bool
		leaderElectionID       string
		defaultRefreshInterval time.Duration
		watchNamespace         string
		stallThreshold         time.Duration
		reconcileTimeout       time.Duration
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the health probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Only a single replica processes CRs when enabled.")
	flag.StringVar(&leaderElectionID, "leader-elect-id", "ferrvault-operator.ferrvault.com",
		"Resource name used for the leader-election lease.")
	flag.DurationVar(&defaultRefreshInterval, "default-refresh-interval", time.Hour,
		"Fallback refresh interval used when a FerrVaultSecret omits spec.refreshInterval.")
	flag.StringVar(&watchNamespace, "watch-namespace", "",
		"Restrict the controller to a single namespace. Empty means cluster-wide.")
	flag.DurationVar(&stallThreshold, "stall-threshold", 15*time.Minute,
		"Fail the liveness probe when no reconcile has completed for this long "+
			"while FerrVault resources exist. Must stay above the connection probe interval.")
	flag.DurationVar(&reconcileTimeout, "reconcile-timeout", 2*time.Minute,
		"Cancel a single reconcile after this long, so one call that never returns cannot "+
			"hold the work queue. Refused below the longest one FerrVault API call can take with its retries.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if err := validateReconcileTimeout(reconcileTimeout); err != nil {
		setupLog.Error(err, "invalid --reconcile-timeout")
		os.Exit(1)
	}

	cacheOpts := cache.Options{}
	if watchNamespace != "" {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{
			watchNamespace: {},
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       leaderElectionID,
		Controller: config.Controller{
			ReconciliationTimeout: reconcileTimeout,
		},
		Cache: cacheOpts,
		// Workloads are read straight from the API server, never through the
		// cache.
		//
		// `rolloutRestart` reads a Deployment/StatefulSet/DaemonSet to annotate
		// its pod template, which is why the operator asks for `get` and
		// `patch` on them and nothing more. But a cached `Get` does not read:
		// it starts an informer for the type, and an informer needs `list` and
		// `watch`. Lacking them, the cache never syncs and the `Get` does not
		// fail — it blocks forever. The reconcile never returns, and the single
		// queue worker stalls behind it.
		//
		// That is not a hypothetical. On 2026-08-25 one added vault key changed
		// a Secret's content, which invoked `rolloutRestart` for the first time
		// in a long while, and the operator stopped reconciling every
		// FerrVaultSecret on the cluster for hours — pod Ready, 4m of CPU, no
		// condition turning false anywhere.
		//
		// Reading uncached makes the declared RBAC exactly sufficient again,
		// and drops a cluster-wide cache of every workload the operator has no
		// use for. The trade is one API call per rollout, on a path that only
		// runs when a secret's content actually changed.
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&appsv1.Deployment{},
					&appsv1.StatefulSet{},
					&appsv1.DaemonSet{},
				},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	broker := controller.NewTokenBroker(mgr.GetClient())
	heartbeat := controller.NewHeartbeat(time.Now())

	if err := (&controller.FerrVaultSecretReconciler{
		Client:                 mgr.GetClient(),
		Scheme:                 mgr.GetScheme(),
		DefaultRefreshInterval: defaultRefreshInterval,
		Broker:                 broker,
		Heartbeat:              heartbeat,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FerrVaultSecret")
		os.Exit(1)
	}

	if err := (&controller.FerrVaultConnectionReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Broker:    broker,
		Heartbeat: heartbeat,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FerrVaultConnection")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("reconcile-progress",
		controller.StallChecker(mgr.GetClient(), heartbeat, stallThreshold)); err != nil {
		setupLog.Error(err, "unable to set up stall check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up readiness check")
		os.Exit(1)
	}

	setupLog.Info("starting manager",
		"watchNamespace", fmtNs(watchNamespace),
		"defaultRefreshInterval", defaultRefreshInterval,
		"stallThreshold", stallThreshold,
		"reconcileTimeout", reconcileTimeout,
	)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func fmtNs(ns string) string {
	if ns == "" {
		return "<cluster-wide>"
	}
	return fmt.Sprintf("%q", ns)
}

func validateReconcileTimeout(d time.Duration) error {
	floor := ferrvault.DefaultRetryPolicy().LongestCall(ferrvault.RequestTimeout)
	if d < floor {
		return fmt.Errorf("%s is below %s, the longest one FerrVault API call can legitimately take "+
			"with its retries; 0 would disable the guardrail and anything shorter cancels healthy reveals",
			d, floor)
	}
	return nil
}

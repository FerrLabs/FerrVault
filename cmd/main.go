// Command manager is the entry point for the ferrflow-operator binary.
//
// It stands up a controller-runtime Manager, registers the CRD types in the
// scheme, wires the FerrFlowSecret reconciler in, and blocks on the
// manager's run loop until the process receives SIGTERM / SIGINT.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ffv1alpha1 "github.com/FerrFlow-Org/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrFlow-Org/FerrFlow-Operator/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ffv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr            string
		probeAddr              string
		enableLeaderElection   bool
		leaderElectionID       string
		defaultRefreshInterval time.Duration
		watchNamespace         string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the health probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Only a single replica processes CRs when enabled.")
	flag.StringVar(&leaderElectionID, "leader-elect-id", "ferrflow-operator.ferrflow.io",
		"Resource name used for the leader-election lease.")
	flag.DurationVar(&defaultRefreshInterval, "default-refresh-interval", time.Hour,
		"Fallback refresh interval used when a FerrFlowSecret omits spec.refreshInterval.")
	flag.StringVar(&watchNamespace, "watch-namespace", "",
		"Restrict the controller to a single namespace. Empty means cluster-wide.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

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
		Cache:                  cacheOpts,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.FerrFlowSecretReconciler{
		Client:                 mgr.GetClient(),
		Scheme:                 mgr.GetScheme(),
		DefaultRefreshInterval: defaultRefreshInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FerrFlowSecret")
		os.Exit(1)
	}

	if err := (&controller.FerrFlowConnectionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FerrFlowConnection")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up readiness check")
		os.Exit(1)
	}

	setupLog.Info("starting manager",
		"watchNamespace", fmtNs(watchNamespace),
		"defaultRefreshInterval", defaultRefreshInterval,
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

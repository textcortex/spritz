package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	var (
		metricsAddr      string
		healthAddr       string
		leaderElection   bool
		leaderElectionID string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8081", "The address the metric endpoint binds to.")
	flag.StringVar(&healthAddr, "health-probe-bind-address", ":8082", "The address the health probe binds to.")
	flag.BoolVar(&leaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&leaderElectionID, "leader-election-id", "spritz-github-app-integration", "Leader election ID.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	scheme.AddKnownTypeWithName(spritzGVK, &unstructured.Unstructured{})

	managerOpts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: healthAddr,
		LeaderElection:         leaderElection,
		LeaderElectionID:       leaderElectionID,
	}
	if cfg.Namespace != "" {
		namespaces := map[string]cache.Config{cfg.Namespace: {}}
		if cfg.PrivateKeyNamespace != "" && cfg.PrivateKeyNamespace != cfg.Namespace {
			namespaces[cfg.PrivateKeyNamespace] = cache.Config{}
		}
		managerOpts.Cache = cache.Options{DefaultNamespaces: namespaces}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manager error: %v\n", err)
		os.Exit(1)
	}

	reconciler := &spritzReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Config:     cfg,
		HTTPClient: &http.Client{},
	}

	if err := reconciler.SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "setup error: %v\n", err)
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		fmt.Fprintf(os.Stderr, "health check error: %v\n", err)
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		fmt.Fprintf(os.Stderr, "ready check error: %v\n", err)
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "manager stopped: %v\n", err)
		os.Exit(1)
	}
}

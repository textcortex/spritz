package main

import (
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	spritzv1 "spritz.sh/operator/api/v1"
	"spritz.sh/operator/controllers"
)

func main() {
	logger := zap.New(zap.UseDevMode(true))
	ctrl.SetLogger(logger)

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(netv1.AddToScheme(scheme))
	utilruntime.Must(gatewayv1.AddToScheme(scheme))
	utilruntime.Must(spritzv1.AddToScheme(scheme))

	cfg, err := config.GetConfig()
	if err != nil {
		logger.Error(err, "unable to load kubeconfig")
		os.Exit(1)
	}

	metricsAddr := envOrDefault("SPRITZ_OPERATOR_METRICS_ADDR", ":8080")
	healthAddr := envOrDefault("SPRITZ_OPERATOR_HEALTH_ADDR", ":8081")

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: healthAddr,
	})
	if err != nil {
		logger.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controllers.SpritzReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller")
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

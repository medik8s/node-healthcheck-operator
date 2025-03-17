/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	// +kubebuilder:scaffold:imports
	"github.com/go-logr/logr"
	"go.uber.org/zap/zapcore"

	corev1 "k8s.io/api/core/v1"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	configv1 "github.com/openshift/api/config/v1"
	consolev1 "github.com/openshift/api/console/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	operatorv1 "github.com/openshift/api/operator/v1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers"
	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/featuregates"
	"github.com/medik8s/node-healthcheck-operator/controllers/initializer"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/resources"
	"github.com/medik8s/node-healthcheck-operator/metrics"
	"github.com/medik8s/node-healthcheck-operator/version"
)

const (
	WebhookCertDir  = "/apiserver.local.config/certificates"
	WebhookCertName = "apiserver.crt"
	WebhookKeyName  = "apiserver.key"
)

var (
	scheme   = pkgruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(remediationv1alpha1.AddToScheme(scheme))

	utilruntime.Must(machinev1beta1.Install(scheme))
	utilruntime.Must(operatorv1.Install(scheme))
	utilruntime.Must(consolev1.Install(scheme))
	utilruntime.Must(configv1.Install(scheme))

	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var enableHTTP2 bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "If HTTP/2 should be enabled for the metrics and webhook servers.")

	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()

	// TLS options for metric and webhook servers:
	// disable HTTP/2 for mitigating relevant CVEs unless configured otherwise
	var tlsOpts []func(*tls.Config)
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			c.NextProtos = []string{"http/1.1"}
		})
		setupLog.Info("HTTP/2 for metrics and webhook server disabled")
	} else {
		setupLog.Info("HTTP/2 for metrics and webhook server enabled")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&corev1.Namespace{}},
			},
		},
		Metrics: server.Options{
			BindAddress: metricsAddr,
			TLSOpts:     tlsOpts,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "e1f13584.medik8s.io",
		WebhookServer:          getWebhookServer(tlsOpts, setupLog),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	caps, err := cluster.NewCapabilities(mgr.GetConfig(), mgr.GetAPIReader(), setupLog, ctx)
	if err != nil {
		setupLog.Error(err, "unable to determine cluster capabilities")
		os.Exit(1)
	}

	setupLog.Info("Cluster capabilities", "IsOnOpenshift", caps.IsOnOpenshift, "HasMachineAPI", caps.HasMachineAPI)

	upgradeChecker, err := cluster.NewClusterUpgradeStatusChecker(mgr, caps)
	if err != nil {
		setupLog.Error(err, "unable initialize cluster upgrade checker")
		os.Exit(1)
	}

	mhcEvents := make(chan event.GenericEvent)
	mhcChecker, err := mhc.NewMHCChecker(mgr, caps, mhcEvents)
	if err != nil {
		setupLog.Error(err, "unable initialize MHC checker")
		os.Exit(1)
	}
	if err = mgr.Add(mhcChecker); err != nil {
		setupLog.Error(err, "failed to add MHC checker to the manager")
		os.Exit(1)
	}

	nhcWatchManager := resources.NewWatchManager(mgr.GetClient(), ctrl.Log.WithName("controllers").WithName("NodeHealthCheck").WithName("WatchManager"), mgr.GetCache())
	if err := (&controllers.NodeHealthCheckReconciler{
		Client:                      mgr.GetClient(),
		Log:                         ctrl.Log.WithName("controllers").WithName("NodeHealthCheck"),
		Recorder:                    mgr.GetEventRecorderFor("NodeHealthCheck"),
		ClusterUpgradeStatusChecker: upgradeChecker,
		MHCChecker:                  mhcChecker,
		Capabilities:                caps,
		MHCEvents:                   mhcEvents,
		WatchManager:                nhcWatchManager,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NodeHealthCheck")
		os.Exit(1)
	}

	if caps.HasMachineAPI {
		featureGateMHCControllerDisabledEvents := make(chan event.GenericEvent)
		featureGateAccessor := featuregates.NewAccessor(mgr.GetConfig(), featureGateMHCControllerDisabledEvents)
		if err = mgr.Add(featureGateAccessor); err != nil {
			setupLog.Error(err, "failed to add feature gate accessor to the manager")
			os.Exit(1)
		}
		mhcWatchManager := resources.NewWatchManager(mgr.GetClient(), ctrl.Log.WithName("controllers").WithName("MachineHealthCheck").WithName("WatchManager"), mgr.GetCache())
		if err := (&controllers.MachineHealthCheckReconciler{
			Client:                         mgr.GetClient(),
			Log:                            ctrl.Log.WithName("controllers").WithName("MachineHealthCheck"),
			Recorder:                       mgr.GetEventRecorderFor("MachineHealthCheck"),
			ClusterUpgradeStatusChecker:    upgradeChecker,
			MHCChecker:                     mhcChecker,
			FeatureGateMHCControllerEvents: featureGateMHCControllerDisabledEvents,
			FeatureGates:                   featureGateAccessor,
			WatchManager:                   mhcWatchManager,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MachineHealthCheck")
			os.Exit(1)
		}
	}

	if err = (&remediationv1alpha1.NodeHealthCheck{}).SetupWebhookWithManager(mgr, &caps); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "NodeHealthCheck")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// Do some initialization
	initializer := initializer.New(mgr, caps, ctrl.Log.WithName("Initializer"))
	if err = mgr.Add(initializer); err != nil {
		setupLog.Error(err, "failed to add initializer to the manager")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Register the MHC specific metrics
	metrics.InitializeNodeHealthCheckMetrics()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getWebhookServer(tlsOpts []func(*tls.Config), log logr.Logger) webhook.Server {

	options := webhook.Options{
		Port:    9443,
		TLSOpts: tlsOpts,
	}

	// check if OLM injected certs exist
	certs := []string{filepath.Join(WebhookCertDir, WebhookCertName), filepath.Join(WebhookCertDir, WebhookKeyName)}
	certsInjected := true
	for _, fname := range certs {
		if _, err := os.Stat(fname); err != nil {
			certsInjected = false
			break
		}
	}
	if certsInjected {
		options.CertDir = WebhookCertDir
		options.CertName = WebhookCertName
		options.KeyName = WebhookKeyName
	} else {
		log.Info("OLM injected certs for webhooks not found")
	}

	return webhook.NewServer(options)
}

func printVersion() {
	setupLog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	setupLog.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	setupLog.Info(fmt.Sprintf("Git Commit: %s", version.GitCommit))
	setupLog.Info(fmt.Sprintf("Build Date: %s", version.BuildDate))
}

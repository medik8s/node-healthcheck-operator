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
	"go.uber.org/zap/zapcore"

	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/openshift/api/console/v1alpha1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	operatorv1 "github.com/openshift/api/operator/v1"

	remediationv1alpha1 "github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers"
	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/initializer"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
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
	utilruntime.Must(v1alpha1.Install(scheme))

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

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		// HEADS UP: once controller runtime is updated and this changes to metrics.Options{},
		// and in case you configure TLS / SecureServing, disable HTTP/2 in it for mitigating related CVEs!
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "e1f13584.medik8s.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	configureWebhookServer(mgr, enableHTTP2)

	upgradeChecker, err := cluster.NewClusterUpgradeStatusChecker(mgr)
	if err != nil {
		setupLog.Error(err, "unable initialize cluster upgrade checker")
		os.Exit(1)
	}

	onOpenshift, err := utils.IsOnOpenshift(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "failed to check if we run on Openshift")
		os.Exit(1)
	}

	mhcEvents := make(chan event.GenericEvent)
	mhcChecker, err := mhc.NewMHCChecker(mgr, onOpenshift, mhcEvents)
	if err != nil {
		setupLog.Error(err, "unable initialize MHC checker")
		os.Exit(1)
	}
	if err = mgr.Add(mhcChecker); err != nil {
		setupLog.Error(err, "failed to add MHC checker to the manager")
		os.Exit(1)
	}

	if err := (&controllers.NodeHealthCheckReconciler{
		Client:                      mgr.GetClient(),
		Log:                         ctrl.Log.WithName("controllers").WithName("NodeHealthCheck"),
		Scheme:                      mgr.GetScheme(),
		Recorder:                    mgr.GetEventRecorderFor("NodeHealthCheck"),
		ClusterUpgradeStatusChecker: upgradeChecker,
		MHCChecker:                  mhcChecker,
		OnOpenShift:                 onOpenshift,
		MHCEvents:                   mhcEvents,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NodeHealthCheck")
		os.Exit(1)
	}

	if onOpenshift {
		if err := (&controllers.MachineHealthCheckReconciler{
			Client:                      mgr.GetClient(),
			Log:                         ctrl.Log.WithName("controllers").WithName("MachineHealthCheck"),
			Scheme:                      mgr.GetScheme(),
			Recorder:                    mgr.GetEventRecorderFor("MachineHealthCheck"),
			ClusterUpgradeStatusChecker: upgradeChecker,
			MHCChecker:                  mhcChecker,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "MachineHealthCheck")
			os.Exit(1)
		}
	}

	if err = (&remediationv1alpha1.NodeHealthCheck{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "NodeHealthCheck")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	ctx := ctrl.SetupSignalHandler()

	// Do some initialization
	initializer := initializer.New(mgr, ctrl.Log.WithName("Initializer"))
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

func printVersion() {
	setupLog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	setupLog.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	setupLog.Info(fmt.Sprintf("Git Commit: %s", version.GitCommit))
	setupLog.Info(fmt.Sprintf("Build Date: %s", version.BuildDate))
}

func configureWebhookServer(mgr ctrl.Manager, enableHTTP2 bool) {

	server := mgr.GetWebhookServer()

	// check for OLM injected certs
	certs := []string{filepath.Join(WebhookCertDir, WebhookCertName), filepath.Join(WebhookCertDir, WebhookKeyName)}
	certsInjected := true
	for _, fname := range certs {
		if _, err := os.Stat(fname); err != nil {
			certsInjected = false
			break
		}
	}
	if certsInjected {
		server.CertDir = WebhookCertDir
		server.CertName = WebhookCertName
		server.KeyName = WebhookKeyName
	} else {
		setupLog.Info("OLM injected certs for webhooks not found")
	}

	// disable http/2 for mitigating relevant CVEs
	if !enableHTTP2 {
		server.TLSOpts = append(server.TLSOpts,
			func(c *tls.Config) {
				c.NextProtos = []string{"http/1.1"}
			},
		)
		setupLog.Info("HTTP/2 for webhooks disabled")
	} else {
		setupLog.Info("HTTP/2 for webhooks enabled")
	}

}

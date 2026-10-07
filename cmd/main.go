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
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"go.uber.org/zap/zapcore"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	pkgruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"

	appv1alpha1 "github.com/medik8s/machine-deletion-remediation/v5/api/v1alpha1"
	"github.com/medik8s/machine-deletion-remediation/v5/internal/controller"
	"github.com/medik8s/machine-deletion-remediation/v5/version"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = pkgruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.Install(scheme))
	utilruntime.Must(appv1alpha1.AddToScheme(scheme))
	utilruntime.Must(machinev1.Install(scheme))
	utilruntime.Must(machinev1beta1.Install(scheme))
	//+kubebuilder:scaffold:scheme
}

//+kubebuilder:rbac:groups=config.openshift.io,resources=apiservers,verbs=get;list;watch

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8443", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()

	// Disable HTTP/2 support to avoid issues with CVE HTTP/2 Rapid Reset.
	// Currently, the metrics server enables/disables HTTP/2 support only if SecureServing is enabled, which is not.
	// Adding the disabling logic anyway to avoid future issues.
	var tlsOpts []func(*tls.Config)
	tlsOpts = append(tlsOpts, func(c *tls.Config) {
		setupLog.Info("disabling HTTP/2 support")
		c.NextProtos = []string{"http/1.1"}
	})

	cfg := ctrl.GetConfigOrDie()
	setupClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "unable to create setup client")
		os.Exit(1)
	}

	isOpenShift := true
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer fetchCancel()
	tlsProfile, err := openshifttls.FetchAPIServerTLSProfile(fetchCtx, setupClient)
	if err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			setupLog.Info("Not on OpenShift, using default TLS settings")
			isOpenShift = false
		} else {
			setupLog.Error(err, "failed to fetch TLS profile")
			os.Exit(1)
		}
	}

	if isOpenShift {
		tlsConfig, unsupported := openshifttls.NewTLSConfigFromProfile(tlsProfile)
		if len(unsupported) > 0 {
			setupLog.Info("Unsupported TLS ciphers ignored", "ciphers", unsupported)
		}
		tlsOpts = append(tlsOpts, tlsConfig)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress:    metricsAddr,
			SecureServing:  true,
			FilterProvider: filters.WithAuthenticationAndAuthorization,
			TLSOpts:        tlsOpts,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "285d4098.example.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.MachineDeletionRemediationReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controller").WithName("MachineDeletionRemediation"),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("MachineDeletionRemediation"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MachineDeletionRemediation")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	if isOpenShift {
		watcher := &openshifttls.SecurityProfileWatcher{
			Client:                mgr.GetClient(),
			InitialTLSProfileSpec: tlsProfile,
			OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
				setupLog.Info("TLS profile changed, restarting")
				cancel()
			},
		}
		if err := watcher.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to set up TLS profile watcher")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

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

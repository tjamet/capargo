/*


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
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/pflag"
	"github.com/tjamet/capargo/pkg/controllers"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/component-base/featuregate"
	"k8s.io/component-base/logs"
	v1 "k8s.io/component-base/logs/api/v1"
	jsonlog "k8s.io/component-base/logs/json"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api/util/flags"
	ctrl "sigs.k8s.io/controller-runtime"
	// +kubebuilder:scaffold:imports
)

var (
	scheme = controllers.Scheme()
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var argocdNamespace string
	var leaderElectionLeaseDuration time.Duration
	var leaderElectionRenewDeadline time.Duration
	var healthAddr string
	var enableByDefault bool

	annotationPrefix := controllers.AnnotationPrefix

	pflag.StringVar(
		&metricsAddr,
		"metrics-addr",
		":8080",
		"The address the metric endpoint binds to.",
	)

	pflag.StringVar(
		&argocdNamespace,
		"argocd-namespace",
		"argocd",
		"The namespace in which ArgoCD runs in which to configure ArgoCD cluster secrets. It defaults to 'argocd'. When provided empty, the argocd cluster will be created in the same namespace as the cluster object.",
	)

	pflag.StringVar(
		&annotationPrefix,
		"metadata-prefix",
		annotationPrefix,
		"The prefix for annotations and labels  used by the controller",
	)

	pflag.BoolVar(
		&enableByDefault,
		"enable-by-default",
		true,
		"Whether to enable or disable creation of argocd clusters by default. This can be changed at the cluster lebel using the label ${metadata-prefix}/argocd: enable|disable",
	)

	pflag.BoolVar(
		&enableLeaderElection,
		"leader-elect",
		false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.",
	)

	pflag.DurationVar(
		&leaderElectionLeaseDuration,
		"leader-elect-lease-duration",
		15*time.Second,
		"Interval at which non-leader candidates will wait to force acquire leadership (duration string)",
	)

	pflag.DurationVar(
		&leaderElectionRenewDeadline,
		"leader-elect-renew-deadline",
		10*time.Second,
		"Duration that the leading controller manager will retry refreshing leadership before giving up (duration string)",
	)
	pflag.StringVar(&healthAddr,
		"health-addr",
		":9440",
		"The address the health endpoint binds to.",
	)

	logOptions := logs.NewOptions()
	diagnosticsOptions := flags.DiagnosticsOptions{}

	featureGate := featuregate.NewFeatureGate()
	featureGate.Add(map[featuregate.Feature]featuregate.FeatureSpec{
		v1.ContextualLogging:    {Default: true, PreRelease: featuregate.GA},
		v1.LoggingStableOptions: {Default: true, PreRelease: featuregate.GA},
	})

	if err := v1.RegisterLogFormat(v1.JSONLogFormat, &jsonlog.Factory{}, v1.LoggingStableOptions); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	logs.AddFlags(pflag.CommandLine, logs.SkipLoggingConfigurationFlags())
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	v1.AddFlags(logOptions, pflag.CommandLine)
	flags.AddDiagnosticsOptions(pflag.CommandLine, &diagnosticsOptions)
	featureGate.AddFlag(pflag.CommandLine)

	pflag.Parse()

	logs.InitLogs()
	if err := v1.ValidateAndApply(logOptions, featureGate); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	diagnosticsOpts := flags.GetDiagnosticsOptions(diagnosticsOptions)

	ctrl.SetLogger(klog.Background())
	ctx := context.Background()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                diagnosticsOpts,
		LeaderElection:         enableLeaderElection,
		LeaseDuration:          &leaderElectionLeaseDuration,
		RenewDeadline:          &leaderElectionRenewDeadline,
		LeaderElectionID:       "controller-leader-elect",
		HealthProbeBindAddress: healthAddr,
	})
	if err != nil {
		klog.FromContext(ctx).Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.ClusterReconciler{
		Client:           mgr.GetClient(),
		ArgoCDNamespace:  argocdNamespace,
		AnnotationPrefix: annotationPrefix,
		EnableByDefault:  enableByDefault,
	}).SetupWithManager(mgr); err != nil {
		klog.FromContext(ctx).WithValues("controller", "ClusterSecret").Error(err, "unable to create controller")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	mgr.AddHealthzCheck("healthz", func(_ *http.Request) error {
		return nil
	})
	mgr.AddReadyzCheck("readyz", func(_ *http.Request) error {
		return nil
	})

	signalCtx := ctrl.SetupSignalHandler()

	klog.FromContext(ctx).Info("starting manager")
	if err := mgr.Start(signalCtx); err != nil {
		klog.FromContext(ctx).Error(err, "problem running manager")
		os.Exit(1)
	}
}

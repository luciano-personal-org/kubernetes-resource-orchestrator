// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package main

import (
	"context"
	"flag"
	"os"
	"time"

	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlrtcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	xv1alpha1 "github.com/awslabs/kro/api/v1alpha1"
	resourcegroupctrl "github.com/awslabs/kro/internal/controller/resourcegroup"
	"github.com/awslabs/kro/internal/graph"
	kroclient "github.com/awslabs/kro/pkg/client"
	"github.com/awslabs/kro/pkg/dynamiccontroller"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(xv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

type customLevelEnabler struct {
	level int
}

func (c customLevelEnabler) Enabled(lvl zapcore.Level) bool {
	return -int(lvl) <= c.level
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var allowCRDDeletion bool
	var resourceGroupConcurrentReconciles int
	var dynamicControllerConcurrentReconciles int
	// reconciler parameters
	var resyncPeriod int
	var queueMaxRetries int
	var shutdownTimeout int
	// var dynamicControllerDefaultResyncPeriod int
	var logLevel int
	var qps float64
	var burst int

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8078", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8079", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&allowCRDDeletion, "allow-crd-deletion", false, "allow kro to delete CRDs")
	flag.IntVar(&resourceGroupConcurrentReconciles, "resource-group-concurrent-reconciles", 1, "The number of resource group reconciles to run in parallel")
	flag.IntVar(&dynamicControllerConcurrentReconciles, "dynamic-controller-concurrent-reconciles", 1, "The number of dynamic controller reconciles to run in parallel")
	// reconciler parametes
	flag.IntVar(&resyncPeriod, "dynamic-controller-default-resync-period", 10,
		"interval at which the controller will re list resources even with no changes, in hours")
	flag.IntVar(&queueMaxRetries, "dynamic-controller-default-queue-max-retries", 20,
		"maximum number of retries for an item in the queue will be retried before being dropped")
	flag.IntVar(&shutdownTimeout, "dynamic-controller-default-shutdown-timeout", 60,
		"maximum duration to wait for the controller to gracefully shutdown, in seconds")
	// log level flags
	flag.IntVar(&logLevel, "log-level", 10, "The log level verbosity. 0 is the least verbose, 5 is the most verbose.")
	// qps and burst
	flag.Float64Var(&qps, "client-qps", 100, "The number of queries per second to allow")
	flag.IntVar(&burst, "client-burst", 150,
		"The number of requests that can be stored for processing before the server starts enforcing the QPS limit")

	flag.Parse()

	opts := zap.Options{
		Development: true,
		Level:       customLevelEnabler{level: logLevel},
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}
	rootLogger := zap.New(zap.UseFlagOptions(&opts))

	ctrl.SetLogger(rootLogger)

	set, err := kroclient.NewSet(kroclient.Config{
		QPS:   float32(qps),
		Burst: burst,
	})
	if err != nil {
		setupLog.Error(err, "unable to create client set")
		os.Exit(1)
	}
	restConfig := set.RESTConfig()

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "6f0f64a5.kro.run",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	dc := dynamiccontroller.NewDynamicController(rootLogger, dynamiccontroller.Config{
		Workers: dynamicControllerConcurrentReconciles,
		// TODO(a-hilaly): expose these as flags
		ShutdownTimeout: time.Duration(shutdownTimeout) * time.Second,
		ResyncPeriod:    time.Duration(resyncPeriod) * time.Hour,
		QueueMaxRetries: queueMaxRetries,
	}, set.Dynamic())

	resourceGroupGraphBuilder, err := graph.NewBuilder(
		restConfig,
	)
	if err != nil {
		setupLog.Error(err, "unable to create resource group graph builder")
		os.Exit(1)
	}

	reconciler := resourcegroupctrl.NewResourceGroupReconciler(
		rootLogger,
		mgr.GetClient(),
		set,
		allowCRDDeletion,
		dc,
		resourceGroupGraphBuilder,
	)
	err = ctrl.NewControllerManagedBy(
		mgr,
	).For(
		&xv1alpha1.ResourceGroup{},
	).WithEventFilter(
		predicate.GenerationChangedPredicate{},
	).WithOptions(
		ctrlrtcontroller.Options{
			MaxConcurrentReconciles: resourceGroupConcurrentReconciles,
		},
	).Complete(reconcile.AsReconciler[*xv1alpha1.ResourceGroup](mgr.GetClient(), reconciler))
	if err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ResourceGroup")
		os.Exit(1)
	}

	go dc.Run(context.Background())

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	go func() {
		if err := mgr.Start(ctx); err != nil {
			setupLog.Error(err, "problem running manager")
			os.Exit(1)
		}
	}()

	<-ctx.Done()

}

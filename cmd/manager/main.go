package main

import (
	"flag"
	"os"
	"time"

	"github.com/lokeshllkumar/kube-balance/controllers"
	"github.com/lokeshllkumar/kube-balance/internal/profiles"
	"github.com/lokeshllkumar/kube-balance/pkg/eviction"
	"github.com/lokeshllkumar/kube-balance/api/v1alpha1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var scheme = runtime.NewScheme()
var setupLog = ctrl.Log.WithName("setup")

func init() {
	utilruntime.Must(clientscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.SchemeBuilder.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var recheckInterval time.Duration
	var maxEvictionsPerNodePerCycle int

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager"+"Enabling this ensures that only one controller manager instance runs at a time")
	flag.DurationVar(&recheckInterval, "recheck-interval", 2 * time.Minute, "Interval for the controller to re-evaluate node/pod states")
	flag.IntVar(&maxEvictionsPerNodePerCycle, "max-evictions-per-node-per-cycle", 1, "Maximum number of pods to evict from a single degraded node per reconcilation cycle")
	flag.Parse()

	// configuring the K8s plugin logger
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{
		Development: true,
	})))

	// setting up the controller manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection: enableLeaderElection,
		LeaderElectionID: "kube-balance-leader-election",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// creating a new Evictor instance to perform pod evictions
	evictor := eviction.NewEvictor(mgr.GetClient(), setupLog.WithName("evictor"))

	// creating a new WorkloadProfileWatcher instance
	profileWatcher := profiles.NewWorkloadProfileWatcher(mgr.GetClient(), mgr.GetCache(), setupLog.WithName("profile-watcher"))

	if err = (&controllers.PodRebalancer{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log: ctrl.Log.WithName("controllers").WithName("PodRebalancer"),
		Evictor: evictor,
		ProfilerWatcher: profileWatcher,
		RecheckInterval: recheckInterval,
		MaxEvictionsPerNodePerCycle: maxEvictionsPerNodePerCycle,
		Recorder: mgr.GetEventRecorderFor("kube-balance-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PodRebalancer")
		os.Exit(1)
	}

	// starting the WorkloadProfileWatcher
	if err := mgr.Add(profileWatcher); err != nil {
		setupLog.Error(err, "unable to add profile watcher to manager")
		os.Exit(1)
	}

	// add helth checks to the manager
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

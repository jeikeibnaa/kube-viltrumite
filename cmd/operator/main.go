package main

import (
	"flag"
	"os"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"github.com/jeikeibnaa/kube-viltrumite/internal/ai/adapters"
	"github.com/jeikeibnaa/kube-viltrumite/internal/controller"
	"github.com/jeikeibnaa/kube-viltrumite/internal/executor"
	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
	"github.com/jeikeibnaa/kube-viltrumite/internal/scanner"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kubeviltrumitev1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var knowledgeBasePath string
	var dryRun bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&knowledgeBasePath, "knowledge-base-path", "/etc/viltrumite/knowledge", "Path to the knowledge base YAML file.")
	flag.BoolVar(&dryRun, "dry-run", false, "Run Helm upgrades in dry-run mode (no changes applied).")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "kube-viltrumite.kubeviltrumite.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	matrix, err := planner.Load(knowledgeBasePath)
	if err != nil {
		setupLog.Error(err, "unable to load knowledge base")
		os.Exit(1)
	}

	provider := os.Getenv("VILTRUMITE_AI_PROVIDER")
	endpoint := os.Getenv("VILTRUMITE_AI_ENDPOINT")
	model := os.Getenv("VILTRUMITE_AI_MODEL")
	apiKey := os.Getenv("VILTRUMITE_AI_KEY")

	var aiProvider interface{}
	switch provider {
	case "ollama":
		aiProvider = adapters.NewOllamaProvider(adapters.OllamaConfig{
			Endpoint: endpoint,
			Model:    model,
		})
	case "anthropic":
		aiProvider = adapters.NewAnthropicProvider(adapters.AnthropicConfig{
			APIKey: apiKey,
			Model:  model,
		})
	case "openai":
		aiProvider = adapters.NewOpenAIProvider(adapters.OpenAIConfig{
			Endpoint: endpoint,
			APIKey:   apiKey,
			Model:    model,
		})
	default:
		aiProvider = adapters.NewNoopProvider()
	}
	_ = aiProvider

	helmExecutor := executor.NewHelmExecutor("", "", dryRun)

	if err := (&controller.StackUpgradeReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Matrix:   matrix,
		Executor: helmExecutor,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "StackUpgrade")
		os.Exit(1)
	}

	if err := (&controller.CompatibilityPolicyReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Scanner: &scanner.ClusterScanner{Client: mgr.GetClient()},
		Matrix:  matrix,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CompatibilityPolicy")
		os.Exit(1)
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
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

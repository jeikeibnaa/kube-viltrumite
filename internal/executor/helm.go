package executor

import (
	"context"
	"fmt"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
)

// HelmExecutor runs Helm upgrade operations against a Kubernetes cluster.
type HelmExecutor struct {
	kubeconfig      string
	namespace       string
	dryRun          bool
	newActionConfig func(namespace string) (*action.Configuration, error)
}

// UpgradeResult holds the outcome of a single upgrade attempt.
type UpgradeResult struct {
	ToolName      string
	FromVersion   string
	ToVersion     string
	Success       bool
	FailureReason string
	RolledBack    bool
	Duration      time.Duration
}

// NewHelmExecutor creates a HelmExecutor that targets the given kubeconfig and namespace.
func NewHelmExecutor(kubeconfig, namespace string, dryRun bool) *HelmExecutor {
	e := &HelmExecutor{
		kubeconfig: kubeconfig,
		namespace:  namespace,
		dryRun:     dryRun,
	}
	e.newActionConfig = e.initActionConfig
	return e
}

func (e *HelmExecutor) initActionConfig(namespace string) (*action.Configuration, error) {
	flags := &genericclioptions.ConfigFlags{
		Namespace: &namespace,
	}
	if e.kubeconfig != "" {
		flags.KubeConfig = &e.kubeconfig
	}
	cfg := new(action.Configuration)
	if err := cfg.Init(flags, namespace, "secret", func(string, ...interface{}) {}); err != nil {
		return nil, fmt.Errorf("executor: init action config: %w", err)
	}
	return cfg, nil
}

// Upgrade performs a helm upgrade for the given step and returns a structured result.
// Pre-flight or chart-load errors are returned as Go errors; upgrade-level failures
// are captured in UpgradeResult (RolledBack=true) with a nil Go error.
func (e *HelmExecutor) Upgrade(ctx context.Context, step planner.UpgradeStep) (*UpgradeResult, error) {
	ns := e.namespace
	if step.Namespace != "" {
		ns = step.Namespace
	}

	cfg, err := e.newActionConfig(ns)
	if err != nil {
		return nil, fmt.Errorf("executor: upgrade %s: action config: %w", step.ReleaseName, err)
	}

	// Step 1: pre-flight — verify release exists and record its current version.
	getAct := action.NewGet(cfg)
	rel, err := getAct.Run(step.ReleaseName)
	if err != nil {
		return nil, fmt.Errorf("release not found: %w", err)
	}
	fromVersion := rel.Chart.Metadata.Version

	// Preserve existing user-supplied values across the upgrade.
	currentValues, err := e.getCurrentValues(cfg, step.ReleaseName)
	if err != nil {
		return nil, fmt.Errorf("executor: get values for %s: %w", step.ReleaseName, err)
	}

	// Step 2: locate and load the target chart.
	upgrade := action.NewUpgrade(cfg)
	upgrade.DryRun = e.dryRun
	upgrade.Wait = true
	upgrade.Timeout = 5 * time.Minute
	upgrade.Atomic = true
	upgrade.ChartPathOptions.Version = step.ToVersion

	settings := cli.New()
	if e.kubeconfig != "" {
		settings.KubeConfig = e.kubeconfig
	}

	chartPath, err := upgrade.LocateChart(step.ChartRef, settings)
	if err != nil {
		return nil, fmt.Errorf("executor: locate chart %s: %w", step.ChartRef, err)
	}
	ch, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("executor: load chart %s: %w", chartPath, err)
	}

	// Step 3: run the upgrade.
	start := time.Now()
	_, upgradeErr := upgrade.Run(step.ReleaseName, ch, currentValues)
	duration := time.Since(start)

	if upgradeErr != nil {
		// Atomic=true means Helm already rolled back; we record that intent.
		return &UpgradeResult{
			ToolName:      step.ToolName,
			FromVersion:   fromVersion,
			ToVersion:     step.ToVersion,
			Success:       false,
			FailureReason: upgradeErr.Error(),
			RolledBack:    true,
			Duration:      duration,
		}, nil
	}

	return &UpgradeResult{
		ToolName:    step.ToolName,
		FromVersion: fromVersion,
		ToVersion:   step.ToVersion,
		Success:     true,
		Duration:    duration,
	}, nil
}

func (e *HelmExecutor) getCurrentValues(cfg *action.Configuration, releaseName string) (map[string]interface{}, error) {
	gv := action.NewGetValues(cfg)
	vals, err := gv.Run(releaseName)
	if err != nil {
		return nil, fmt.Errorf("get values: %w", err)
	}
	return vals, nil
}

// GetCurrentValues fetches the user-supplied values for an existing release.
func (e *HelmExecutor) GetCurrentValues(ctx context.Context, releaseName, namespace string) (map[string]interface{}, error) {
	cfg, err := e.newActionConfig(namespace)
	if err != nil {
		return nil, fmt.Errorf("executor: get values %s: action config: %w", releaseName, err)
	}
	gv := action.NewGetValues(cfg)
	vals, err := gv.Run(releaseName)
	if err != nil {
		return nil, fmt.Errorf("executor: get values %s: %w", releaseName, err)
	}
	return vals, nil
}

// RollbackToVersion rolls back a named release to a specific revision number.
func (e *HelmExecutor) RollbackToVersion(ctx context.Context, releaseName, namespace string, revision int) error {
	cfg, err := e.newActionConfig(namespace)
	if err != nil {
		return fmt.Errorf("executor: rollback %s: action config: %w", releaseName, err)
	}
	rb := action.NewRollback(cfg)
	rb.Version = revision
	rb.Wait = true
	rb.Timeout = 3 * time.Minute
	if err := rb.Run(releaseName); err != nil {
		return fmt.Errorf("executor: rollback %s to revision %d: %w", releaseName, revision, err)
	}
	return nil
}

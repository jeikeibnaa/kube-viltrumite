package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
	"github.com/jeikeibnaa/kube-viltrumite/internal/scanner"
)

// CompatibilityPolicyReconciler reconciles a CompatibilityPolicy object.
type CompatibilityPolicyReconciler struct {
	Client          client.Client
	Scheme          *runtime.Scheme
	Scanner         *scanner.ClusterScanner
	WorkloadScanner *scanner.WorkloadScanner
	Matrix          *planner.Matrix
	Detections      []scanner.ToolDetection
}

//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=compatibilitypolicies,verbs=get;list;watch
//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=compatibilitypolicies/status,verbs=update;patch
//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=stackupgrades,verbs=get;list;create
//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=stackupgrades/status,verbs=update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments;daemonsets,verbs=list;watch

func (r *CompatibilityPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var policy kubeviltrumitev1alpha1.CompatibilityPolicy
	if err := r.Client.Get(ctx, req.NamespacedName, &policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.Scanner == nil || r.Matrix == nil {
		logger.Info("scanner or matrix not configured, skipping")
		return r.requeue(&policy), nil
	}

	// Step 1: determine tool list and operating mode.
	known := r.Matrix.ListTools()
	knownSet := make(map[string]bool, len(known))
	for _, n := range known {
		knownSet[n] = true
	}

	var toEval []string
	status := kubeviltrumitev1alpha1.CompatibilityPolicyStatus{}

	if len(policy.Spec.TrackedTools) == 0 {
		status.Mode = "discovery"
		toEval = known
	} else {
		status.Mode = "focused"
		for _, n := range policy.Spec.TrackedTools {
			if knownSet[n] {
				toEval = append(toEval, n)
			} else {
				status.UntrackableTools = append(status.UntrackableTools, n)
			}
		}
	}

	// Step 2: scan once, build installed map keyed by tool name.
	clusterTools, err := r.Scanner.ScanAll(ctx, policy.Spec.WatchNamespaces)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cluster scan: %w", err)
	}

	// Raw results are added first; helm/flux/argocd results overwrite them
	// because those sources are upgradeable via package manager.
	installed := make(map[string]scanner.InstalledTool)

	if r.WorkloadScanner != nil {
		rawTools, rawErr := r.WorkloadScanner.ScanWorkloads(ctx, policy.Spec.WatchNamespaces, r.Detections)
		if rawErr != nil {
			logger.Error(rawErr, "workload scan failed, continuing without raw results")
		} else {
			for _, t := range rawTools {
				installed[t.Name] = t
			}
		}
	}

	for _, t := range clusterTools {
		installed[t.Name] = t
	}

	// Collect tools installed in the cluster that have no KB entry (blind-spot signal).
	for name := range installed {
		if !knownSet[name] {
			status.UnknownInstalled = append(status.UnknownInstalled, name)
		}
	}
	sort.Strings(status.UnknownInstalled)

	// Default RiskTolerance to HIGH when unset to avoid silently hiding all upgrades.
	tolerance := policy.Spec.RiskTolerance
	if tolerance == "" {
		tolerance = ai.RiskHigh
	}

	// Step 3: build TrackedToolStatus for each evaluated tool.
	for _, toolName := range toEval {
		ts := kubeviltrumitev1alpha1.TrackedToolStatus{Name: toolName}
		if t, ok := installed[toolName]; ok {
			ts.Installed = true
			ts.InstalledVersion = t.CurrentVersion
			ts.Source = t.Source
			ts.Namespace = t.Namespace

			rec, risk, found := r.Matrix.LatestSafeVersion(toolName, t.CurrentVersion, tolerance)
			if found {
				ts.UpgradeAvailable = true
				ts.RecommendedVersion = rec
				ts.Risk = risk
				ts.Message = "upgrade available"
			} else {
				ts.Message = "up to date"
			}
		} else {
			ts.Message = "not currently installed in cluster"
		}
		status.Tools = append(status.Tools, ts)
	}

	// Step 4: push — create StackUpgrades only when autoplan is explicitly enabled.
	if policy.Spec.Autoplan != nil && policy.Spec.Autoplan.Enabled {
		maxRisk := policy.Spec.Autoplan.MaxRisk
		if maxRisk == "" {
			maxRisk = ai.RiskLow
		}
		for _, ts := range status.Tools {
			if !ts.UpgradeAvailable {
				continue
			}
			if !planner.RiskAtOrBelow(ts.Risk, maxRisk) {
				continue
			}
			upgradeName := sanitizeName("auto-" + ts.Name + "-" + ts.RecommendedVersion)
			var existing kubeviltrumitev1alpha1.StackUpgrade
			err := r.Client.Get(ctx, types.NamespacedName{Name: upgradeName, Namespace: policy.Namespace}, &existing)
			if err == nil {
				continue // already exists
			}
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}

			upgrade := &kubeviltrumitev1alpha1.StackUpgrade{
				ObjectMeta: metav1.ObjectMeta{
					Name:      upgradeName,
					Namespace: policy.Namespace,
				},
				Spec: kubeviltrumitev1alpha1.StackUpgradeSpec{
					Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
						{
							Name:           ts.Name,
							CurrentVersion: ts.InstalledVersion,
							TargetVersion:  ts.RecommendedVersion,
							Risk:           ts.Risk,
						},
					},
					ApprovalRequired: !policy.Spec.Autoplan.AutoApprove,
				},
			}

			if err := r.Client.Create(ctx, upgrade); err != nil {
				return ctrl.Result{}, fmt.Errorf("create StackUpgrade %s: %w", upgradeName, err)
			}
			logger.Info("created StackUpgrade", "name", upgradeName, "tool", ts.Name, "targetVersion", ts.RecommendedVersion)

			if policy.Spec.Autoplan.AutoApprove {
				upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseApproved
				if err := r.Client.Status().Update(ctx, upgrade); err != nil {
					logger.Error(err, "failed to set auto-approved status", "name", upgradeName)
				}
			}
		}
	}

	// Step 5: persist status.
	now := metav1.Now()
	status.LastScanTime = &now
	policy.Status = status
	if err := r.Client.Status().Update(ctx, &policy); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}

	return r.requeue(&policy), nil
}

func (r *CompatibilityPolicyReconciler) requeue(policy *kubeviltrumitev1alpha1.CompatibilityPolicy) ctrl.Result {
	interval := policy.Spec.ScanInterval.Duration
	if interval == 0 {
		interval = 5 * time.Minute
	}
	return ctrl.Result{RequeueAfter: interval}
}

// SetupWithManager sets up the controller with the Manager.
func (r *CompatibilityPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeviltrumitev1alpha1.CompatibilityPolicy{}).
		Complete(r)
}

// sanitizeName converts s to a valid Kubernetes DNS subdomain name (RFC 1123).
func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-.")
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	if len(result) > 253 {
		result = strings.TrimRight(result[:253], "-.")
	}
	return result
}

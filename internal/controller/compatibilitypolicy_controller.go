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
//+kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=list;watch

// Scanner reads. Flux HelmReleases and Argo CD Applications go through the
// unstructured client; plain Helm releases are stored as Secrets, which the
// Helm SDK lists directly (the HelmExecutor reads them too).
//+kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch
//+kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list

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

	var toEval []string
	status := kubeviltrumitev1alpha1.CompatibilityPolicyStatus{}

	if len(policy.Spec.TrackedTools) == 0 {
		status.Mode = "discovery"
		toEval = known
	} else {
		status.Mode = "focused"
		// A tracked name may be an alias ("argocd", "kube-prometheus-stack").
		evaluated := make(map[string]bool)
		for _, n := range policy.Spec.TrackedTools {
			canonical, ok := r.Matrix.CanonicalName(n)
			switch {
			case !ok:
				status.UntrackableTools = append(status.UntrackableTools, n)
			case !evaluated[canonical]:
				evaluated[canonical] = true
				toEval = append(toEval, canonical)
			}
		}
	}

	// Step 2: scan once, then map every find to a knowledge-base tool.
	found, err := r.Scanner.ScanAll(ctx, policy.Spec.WatchNamespaces)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cluster scan: %w", err)
	}
	if r.WorkloadScanner != nil {
		rawTools, rawErr := r.WorkloadScanner.ScanWorkloads(ctx, policy.Spec.WatchNamespaces, r.Detections)
		if rawErr != nil {
			logger.Error(rawErr, "workload scan failed, continuing without raw results")
		} else {
			found = append(found, rawTools...)
		}
	}
	installed, unknown := resolveInstalled(r.Matrix, r.Detections, found)
	// Installed charts with no knowledge-base entry (blind-spot signal).
	status.UnknownInstalled = unknown

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

			switch {
			case t.CurrentVersion == "":
				// Nothing to compare, so no claim that the tool is up to date.
				ts.Message = "installed version unknown"
			case !planner.ValidVersion(t.CurrentVersion):
				ts.Message = fmt.Sprintf("installed version %q not recognised", t.CurrentVersion)
			default:
				rec, risk, found := r.Matrix.LatestSafeVersion(toolName, t.CurrentVersion, tolerance)
				if found {
					ts.UpgradeAvailable = true
					ts.RecommendedVersion = rec
					ts.Risk = risk
					ts.Message = "upgrade available"
				} else {
					ts.Message = "up to date"
				}
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

// sourceRank orders how a tool is managed. A GitOps controller owns the
// releases it manages, plain Helm comes next and a raw install last, so the
// report names the source an upgrade has to go through.
var sourceRank = map[string]int{"fluxcd": 3, "argocd": 3, "helm": 2, "raw": 1}

// resolveInstalled maps each scanned find to its knowledge-base tool and
// keeps one record per tool: the best-ranked source and, between equals, one
// with a version the matrix can compare. It returns the records keyed by tool
// and, sorted, the names of finds that matched no tool.
func resolveInstalled(m *planner.Matrix, detections []scanner.ToolDetection, found []scanner.InstalledTool) (map[string]scanner.InstalledTool, []string) {
	byTool := make(map[string]scanner.ToolDetection, len(detections))
	for _, d := range detections {
		byTool[d.ToolName] = d
	}

	installed := make(map[string]scanner.InstalledTool)
	unknown := make(map[string]bool)
	for _, t := range found {
		name, ok := identify(m, t)
		if !ok {
			unknown[t.Name] = true
			continue
		}
		t.Name = name
		t.CurrentVersion = appVersion(m, byTool[name], t)
		if prev, seen := installed[name]; !seen || preferred(t, prev) {
			installed[name] = t
		}
	}

	names := make([]string, 0, len(unknown))
	for n := range unknown {
		names = append(names, n)
	}
	sort.Strings(names)
	return installed, names
}

// identify returns the knowledge-base tool a find is. A raw find is already
// named by its detection rule; the others report a chart name.
func identify(m *planner.Matrix, t scanner.InstalledTool) (string, bool) {
	if t.Source == "raw" {
		return m.CanonicalName(t.Name)
	}
	chart := t.ChartName
	if chart == "" {
		chart = t.Name
	}
	return m.IdentifyChart(chart, t.ChartOrigins)
}

// appVersion returns a find's app version: what its source reported, else the
// version of a known image it runs (Argo CD reports these), else the app
// version the knowledge base records for its exact chart version.
func appVersion(m *planner.Matrix, det scanner.ToolDetection, t scanner.InstalledTool) string {
	if t.CurrentVersion != "" {
		return t.CurrentVersion
	}
	if v := scanner.VersionFromImages(t.Images, det); v != "" {
		return v
	}
	if v, ok := m.AppVersionForChart(t.Name, t.ChartVersion); ok {
		return v
	}
	return ""
}

// preferred reports whether find a should replace b as the record of a tool.
func preferred(a, b scanner.InstalledTool) bool {
	if sourceRank[a.Source] != sourceRank[b.Source] {
		return sourceRank[a.Source] > sourceRank[b.Source]
	}
	return planner.ValidVersion(a.CurrentVersion) && !planner.ValidVersion(b.CurrentVersion)
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

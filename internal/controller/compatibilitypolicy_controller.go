package controller

import (
	"context"
	"fmt"
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
	Client  client.Client
	Scheme  *runtime.Scheme
	Scanner *scanner.ClusterScanner
	Matrix  *planner.Matrix
}

//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=compatibilitypolicies,verbs=get;list;watch
//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=stackupgrades,verbs=get;list;create

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

	tools, err := r.Scanner.ScanHelmReleases(ctx, policy.Spec.WatchNamespaces)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("scan: %w", err)
	}

	for _, tool := range tools {
		latestVersion, ok := r.Matrix.LatestVersion(tool.Name)
		if !ok || latestVersion == tool.CurrentVersion {
			continue
		}

		entry, err := r.Matrix.Resolve(tool.Name, tool.CurrentVersion, latestVersion)
		if err != nil {
			logger.Info("matrix resolve failed", "tool", tool.Name, "err", err)
			continue
		}

		upgradeName := fmt.Sprintf("auto-%s-%s", tool.Name, latestVersion)
		var existing kubeviltrumitev1alpha1.StackUpgrade
		err = r.Client.Get(ctx, types.NamespacedName{Name: upgradeName, Namespace: policy.Namespace}, &existing)
		if err == nil {
			continue
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
						Name:           tool.Name,
						CurrentVersion: tool.CurrentVersion,
						TargetVersion:  latestVersion,
						Risk:           ai.RiskLevel(entry.RiskLevel),
					},
				},
				ApprovalRequired: true,
			},
		}

		if err := r.Client.Create(ctx, upgrade); err != nil {
			return ctrl.Result{}, fmt.Errorf("create StackUpgrade %s: %w", upgradeName, err)
		}
		logger.Info("created StackUpgrade", "name", upgradeName, "tool", tool.Name, "targetVersion", latestVersion)
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

package controller

import (
	"context"
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"github.com/jeikeibnaa/kube-viltrumite/internal/executor"
	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
)

// Upgrader executes a single Helm release upgrade operation.
type Upgrader interface {
	Upgrade(ctx context.Context, step planner.UpgradeStep) (*executor.UpgradeResult, error)
}

// StackUpgradeReconciler reconciles a StackUpgrade object.
type StackUpgradeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Matrix   *planner.Matrix
	Executor Upgrader
}

//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=stackupgrades,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeviltrumite.io,resources=stackupgrades/status,verbs=get;update;patch

func (r *StackUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var upgrade kubeviltrumitev1alpha1.StackUpgrade
	if err := r.Get(ctx, req.NamespacedName, &upgrade); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	switch upgrade.Status.Phase {
	case kubeviltrumitev1alpha1.UpgradePhasePending:
		return r.reconcilePending(ctx, &upgrade)
	case kubeviltrumitev1alpha1.UpgradePhaseApproved:
		return r.reconcileApproved(ctx, &upgrade)
	case kubeviltrumitev1alpha1.UpgradePhaseUpgrading:
		return r.reconcileUpgrading(ctx, &upgrade)
	case kubeviltrumitev1alpha1.UpgradePhaseFailed:
		return r.reconcileFailed(ctx, &upgrade)
	default:
		upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhasePending
		if err := r.Status().Update(ctx, &upgrade); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
}

func (r *StackUpgradeReconciler) reconcilePending(ctx context.Context, upgrade *kubeviltrumitev1alpha1.StackUpgrade) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if r.Matrix != nil {
		for _, tool := range upgrade.Spec.Tools {
			if _, err := r.Matrix.Resolve(tool.Name, tool.CurrentVersion, tool.TargetVersion); err != nil {
				logger.Info("unknown tool in knowledge base", "tool", tool.Name)
				apimeta.SetStatusCondition(&upgrade.Status.Conditions, metav1.Condition{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					Reason:             "UnknownTool",
					Message:            fmt.Sprintf("tool %q not found in knowledge base: %v", tool.Name, err),
					LastTransitionTime: metav1.Now(),
				})
				if err := r.Status().Update(ctx, upgrade); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
		}
	}

	if upgrade.Spec.ApprovalRequired {
		apimeta.SetStatusCondition(&upgrade.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "WaitingForApproval",
			Message:            "upgrade requires manual approval",
			LastTransitionTime: metav1.Now(),
		})
		if err := r.Status().Update(ctx, upgrade); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseApproved
	if err := r.Status().Update(ctx, upgrade); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *StackUpgradeReconciler) reconcileApproved(ctx context.Context, upgrade *kubeviltrumitev1alpha1.StackUpgrade) (ctrl.Result, error) {
	now := metav1.Now()
	upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseUpgrading
	upgrade.Status.StartedAt = &now
	upgrade.Status.TotalSteps = len(upgrade.Spec.Tools)
	upgrade.Status.CurrentStep = 0
	if err := r.Status().Update(ctx, upgrade); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *StackUpgradeReconciler) reconcileUpgrading(ctx context.Context, upgrade *kubeviltrumitev1alpha1.StackUpgrade) (ctrl.Result, error) {
	currentStep := upgrade.Status.CurrentStep
	if currentStep >= len(upgrade.Spec.Tools) {
		now := metav1.Now()
		upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseSucceeded
		upgrade.Status.CompletedAt = &now
		apimeta.SetStatusCondition(&upgrade.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "UpgradeComplete",
			Message:            "all tools upgraded successfully",
			LastTransitionTime: metav1.Now(),
		})
		if err := r.Status().Update(ctx, upgrade); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	step := upgrade.Spec.Tools[currentStep]
	planStep := planner.UpgradeStep{
		ToolName:    step.Name,
		ReleaseName: step.Name,
		ChartRef:    step.Name,
		FromVersion: step.CurrentVersion,
		ToVersion:   step.TargetVersion,
	}

	result, err := r.Executor.Upgrade(ctx, planStep)
	if err != nil {
		upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseFailed
		upgrade.Status.FailureReason = err.Error()
		apimeta.SetStatusCondition(&upgrade.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "UpgradeError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		if statusErr := r.Status().Update(ctx, upgrade); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, nil
	}

	if !result.Success {
		if result.RolledBack {
			upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseRolledBack
		} else {
			upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseFailed
		}
		upgrade.Status.FailureReason = result.FailureReason
		if statusErr := r.Status().Update(ctx, upgrade); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, nil
	}

	upgrade.Status.CurrentStep++
	apimeta.SetStatusCondition(&upgrade.Status.Conditions, metav1.Condition{
		Type:               "Progressing",
		Status:             metav1.ConditionTrue,
		Reason:             "StepComplete",
		Message:            fmt.Sprintf("Upgraded %s to %s", step.Name, step.TargetVersion),
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, upgrade); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *StackUpgradeReconciler) reconcileFailed(ctx context.Context, upgrade *kubeviltrumitev1alpha1.StackUpgrade) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("upgrade failed, would rollback")
	upgrade.Status.Phase = kubeviltrumitev1alpha1.UpgradePhaseRolledBack
	if err := r.Status().Update(ctx, upgrade); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StackUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeviltrumitev1alpha1.StackUpgrade{}).
		Complete(r)
}

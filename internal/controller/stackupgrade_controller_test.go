package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"github.com/jeikeibnaa/kube-viltrumite/internal/executor"
	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
)

type fakeUpgrader struct {
	result *executor.UpgradeResult
	err    error
}

func (f *fakeUpgrader) Upgrade(_ context.Context, _ planner.UpgradeStep) (*executor.UpgradeResult, error) {
	return f.result, f.err
}

func successUpgrader() *fakeUpgrader {
	return &fakeUpgrader{result: &executor.UpgradeResult{Success: true}}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme clientgo: %v", err)
	}
	if err := kubeviltrumitev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}
	return s
}

func reconcileOnce(t *testing.T, r *StackUpgradeReconciler, name, ns string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

func getUpgrade(t *testing.T, r *StackUpgradeReconciler, name, ns string) kubeviltrumitev1alpha1.StackUpgrade {
	t.Helper()
	var obj kubeviltrumitev1alpha1.StackUpgrade
	if err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, &obj); err != nil {
		t.Fatalf("Get: %v", err)
	}
	return obj
}

// 1. empty phase → sets Pending
func TestReconcile_EmptyPhase_SetsPending(t *testing.T) {
	scheme := newTestScheme(t)
	obj := &kubeviltrumitev1alpha1.StackUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	r := &StackUpgradeReconciler{Client: c, Scheme: scheme}

	reconcileOnce(t, r, "test", "default")

	result := getUpgrade(t, r, "test", "default")
	if result.Status.Phase != kubeviltrumitev1alpha1.UpgradePhasePending {
		t.Errorf("Phase: got %q, want %q", result.Status.Phase, kubeviltrumitev1alpha1.UpgradePhasePending)
	}
}

// 2. Pending + ApprovalRequired=false → sets Approved
func TestReconcile_Pending_NoApproval_SetsApproved(t *testing.T) {
	scheme := newTestScheme(t)
	obj := &kubeviltrumitev1alpha1.StackUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       kubeviltrumitev1alpha1.StackUpgradeSpec{ApprovalRequired: false},
		Status:     kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: kubeviltrumitev1alpha1.UpgradePhasePending},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	r := &StackUpgradeReconciler{Client: c, Scheme: scheme}

	reconcileOnce(t, r, "test", "default")

	result := getUpgrade(t, r, "test", "default")
	if result.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseApproved {
		t.Errorf("Phase: got %q, want %q", result.Status.Phase, kubeviltrumitev1alpha1.UpgradePhaseApproved)
	}
}

// 3. Approved → sets Upgrading with correct TotalSteps
func TestReconcile_Approved_SetsUpgrading(t *testing.T) {
	scheme := newTestScheme(t)
	obj := &kubeviltrumitev1alpha1.StackUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: kubeviltrumitev1alpha1.StackUpgradeSpec{
			Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
				{Name: "cert-manager", CurrentVersion: "1.12", TargetVersion: "1.13"},
				{Name: "ingress-nginx", CurrentVersion: "1.3", TargetVersion: "1.4"},
			},
		},
		Status: kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: kubeviltrumitev1alpha1.UpgradePhaseApproved},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	r := &StackUpgradeReconciler{Client: c, Scheme: scheme}

	reconcileOnce(t, r, "test", "default")

	result := getUpgrade(t, r, "test", "default")
	if result.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseUpgrading {
		t.Errorf("Phase: got %q, want %q", result.Status.Phase, kubeviltrumitev1alpha1.UpgradePhaseUpgrading)
	}
	if result.Status.TotalSteps != 2 {
		t.Errorf("TotalSteps: got %d, want 2", result.Status.TotalSteps)
	}
	if result.Status.CurrentStep != 0 {
		t.Errorf("CurrentStep: got %d, want 0", result.Status.CurrentStep)
	}
	if result.Status.StartedAt == nil {
		t.Error("StartedAt: expected non-nil")
	}
}

// 4. Upgrading with CurrentStep < TotalSteps → increments step
func TestReconcile_Upgrading_IncrementsStep(t *testing.T) {
	scheme := newTestScheme(t)
	obj := &kubeviltrumitev1alpha1.StackUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: kubeviltrumitev1alpha1.StackUpgradeSpec{
			Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
				{Name: "cert-manager", CurrentVersion: "1.12", TargetVersion: "1.13"},
				{Name: "ingress-nginx", CurrentVersion: "1.3", TargetVersion: "1.4"},
			},
		},
		Status: kubeviltrumitev1alpha1.StackUpgradeStatus{
			Phase:       kubeviltrumitev1alpha1.UpgradePhaseUpgrading,
			TotalSteps:  2,
			CurrentStep: 0,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	r := &StackUpgradeReconciler{Client: c, Scheme: scheme, Executor: successUpgrader()}

	reconcileOnce(t, r, "test", "default")

	result := getUpgrade(t, r, "test", "default")
	if result.Status.CurrentStep != 1 {
		t.Errorf("CurrentStep: got %d, want 1", result.Status.CurrentStep)
	}
	if result.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseUpgrading {
		t.Errorf("Phase: got %q, want %q", result.Status.Phase, kubeviltrumitev1alpha1.UpgradePhaseUpgrading)
	}
}

// 5. Upgrading with CurrentStep = len(tools) (all steps done) → sets Succeeded
func TestReconcile_Upgrading_LastStep_SetsSucceeded(t *testing.T) {
	scheme := newTestScheme(t)
	obj := &kubeviltrumitev1alpha1.StackUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: kubeviltrumitev1alpha1.StackUpgradeSpec{
			Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
				{Name: "cert-manager", CurrentVersion: "1.12", TargetVersion: "1.13"},
			},
		},
		Status: kubeviltrumitev1alpha1.StackUpgradeStatus{
			Phase:       kubeviltrumitev1alpha1.UpgradePhaseUpgrading,
			TotalSteps:  1,
			CurrentStep: 1, // all tools already upgraded; next reconcile should mark Succeeded
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	r := &StackUpgradeReconciler{Client: c, Scheme: scheme}

	reconcileOnce(t, r, "test", "default")

	result := getUpgrade(t, r, "test", "default")
	if result.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseSucceeded {
		t.Errorf("Phase: got %q, want %q", result.Status.Phase, kubeviltrumitev1alpha1.UpgradePhaseSucceeded)
	}
	if result.Status.CompletedAt == nil {
		t.Error("CompletedAt: expected non-nil")
	}

	var readyCond *metav1.Condition
	for i := range result.Status.Conditions {
		if result.Status.Conditions[i].Type == "Ready" {
			readyCond = &result.Status.Conditions[i]
			break
		}
	}
	if readyCond == nil {
		t.Fatal("Ready condition not found")
	}
	if readyCond.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition status: got %q, want True", readyCond.Status)
	}
	if readyCond.Reason != "UpgradeComplete" {
		t.Errorf("Ready condition reason: got %q, want UpgradeComplete", readyCond.Reason)
	}
}

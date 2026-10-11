package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
	"github.com/jeikeibnaa/kube-viltrumite/internal/executor"
	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
)

// fakeUpgrader returns result and err, and records the steps it was asked to run.
type fakeUpgrader struct {
	result *executor.UpgradeResult
	err    error
	steps  []planner.UpgradeStep
}

func (f *fakeUpgrader) Upgrade(_ context.Context, step planner.UpgradeStep) (*executor.UpgradeResult, error) {
	f.steps = append(f.steps, step)
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

// helmTool is a plan step for a tool installed with plain Helm, as autoplan
// writes one.
func helmTool(name, current, target, chartVersion string) kubeviltrumitev1alpha1.ToolUpgradeSpec {
	return kubeviltrumitev1alpha1.ToolUpgradeSpec{
		Name: name, CurrentVersion: current, TargetVersion: target, Risk: ai.RiskLow,
		Source: "helm", Namespace: name, ReleaseName: name,
		Chart: &kubeviltrumitev1alpha1.ChartRef{Name: name, Version: chartVersion, Repository: "https://charts.example.com"},
	}
}

// newUpgradeReconciler returns a reconciler over a fake API server that holds
// upgrade and serves its status as a subresource, as the CRD does.
func newUpgradeReconciler(t *testing.T, upgrade *kubeviltrumitev1alpha1.StackUpgrade, funcs interceptor.Funcs) *StackUpgradeReconciler {
	t.Helper()
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(upgrade).WithStatusSubresource(upgrade).WithInterceptorFuncs(funcs).Build()
	return &StackUpgradeReconciler{Client: c, Scheme: scheme}
}

func newUpgrade(spec kubeviltrumitev1alpha1.StackUpgradeSpec, status kubeviltrumitev1alpha1.StackUpgradeStatus) *kubeviltrumitev1alpha1.StackUpgrade {
	return &kubeviltrumitev1alpha1.StackUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       spec,
		Status:     status,
	}
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

// TestReconcile_Approval covers the approval gate: a plan waits in Pending,
// with no requeue, until spec.approved is set; the watch delivers that spec
// change. A plan whose tool the knowledge base does not cover waits even when
// approved. An approved plan no longer reads as waiting for approval.
func TestReconcile_Approval(t *testing.T) {
	m, _ := embeddedKB(t)
	tests := []struct {
		name       string
		approved   bool
		phase      kubeviltrumitev1alpha1.UpgradePhase
		tool       kubeviltrumitev1alpha1.ToolUpgradeSpec
		wantPhase  kubeviltrumitev1alpha1.UpgradePhase
		wantReason string // the Ready condition's; "" means none
		wantResult ctrl.Result
	}{
		{
			name:       "new plan, not approved: Pending",
			tool:       helmTool("vault", "1.14.0", "2.0.4", "0.34.1"),
			wantPhase:  kubeviltrumitev1alpha1.UpgradePhasePending,
			wantReason: "WaitingForApproval",
		},
		{
			name:       "Pending, still not approved",
			phase:      kubeviltrumitev1alpha1.UpgradePhasePending,
			tool:       helmTool("vault", "1.14.0", "2.0.4", "0.34.1"),
			wantPhase:  kubeviltrumitev1alpha1.UpgradePhasePending,
			wantReason: "WaitingForApproval",
		},
		{
			name:       "new plan, created approved: Approved",
			approved:   true,
			tool:       helmTool("vault", "1.14.0", "2.0.4", "0.34.1"),
			wantPhase:  kubeviltrumitev1alpha1.UpgradePhaseApproved,
			wantReason: "Approved",
			wantResult: ctrl.Result{RequeueAfter: stepInterval},
		},
		{
			name:       "Pending, then approved: Approved",
			approved:   true,
			phase:      kubeviltrumitev1alpha1.UpgradePhasePending,
			tool:       helmTool("vault", "1.14.0", "2.0.4", "0.34.1"),
			wantPhase:  kubeviltrumitev1alpha1.UpgradePhaseApproved,
			wantReason: "Approved",
			wantResult: ctrl.Result{RequeueAfter: stepInterval},
		},
		{
			name:       "approved, but the knowledge base does not know the tool",
			approved:   true,
			tool:       helmTool("velero", "1.13.0", "1.14.0", "7.0.0"),
			wantPhase:  kubeviltrumitev1alpha1.UpgradePhasePending,
			wantReason: "UnknownTool",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: tc.phase}
			if tc.phase == kubeviltrumitev1alpha1.UpgradePhasePending {
				// What the last reconcile left, while the plan waited.
				status.Conditions = []metav1.Condition{{
					Type: "Ready", Status: metav1.ConditionFalse, Reason: "WaitingForApproval",
					Message: "set spec.approved to carry out the plan", LastTransitionTime: metav1.Now(),
				}}
			}
			upgrade := newUpgrade(
				kubeviltrumitev1alpha1.StackUpgradeSpec{Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{tc.tool}, Approved: tc.approved},
				status)
			r := newUpgradeReconciler(t, upgrade, interceptor.Funcs{})
			r.Matrix = m

			res := reconcileOnce(t, r, "test", "default")

			got := getUpgrade(t, r, "test", "default")
			if got.Status.Phase != tc.wantPhase {
				t.Errorf("phase %q, want %q", got.Status.Phase, tc.wantPhase)
			}
			reason := ""
			if c := apimeta.FindStatusCondition(got.Status.Conditions, "Ready"); c != nil {
				reason = c.Reason
			}
			if reason != tc.wantReason {
				t.Errorf("Ready reason %q, want %q", reason, tc.wantReason)
			}
			if res != tc.wantResult {
				t.Errorf("result %+v, want %+v", res, tc.wantResult)
			}
		})
	}
}

// TestReconcile_Approved_SetsUpgrading checks the start of the run.
func TestReconcile_Approved_SetsUpgrading(t *testing.T) {
	upgrade := newUpgrade(kubeviltrumitev1alpha1.StackUpgradeSpec{
		Approved: true,
		Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
			helmTool("cert-manager", "v1.20.4", "1.21.0", "v1.21.0"),
			helmTool("vault", "1.14.0", "2.0.4", "0.34.1"),
		},
	}, kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: kubeviltrumitev1alpha1.UpgradePhaseApproved})
	r := newUpgradeReconciler(t, upgrade, interceptor.Funcs{})

	res := reconcileOnce(t, r, "test", "default")

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
	if c := apimeta.FindStatusCondition(result.Status.Conditions, "Ready"); c == nil || c.Reason != "Upgrading" {
		t.Errorf("Ready condition %+v, want reason Upgrading", c)
	}
	if res != (ctrl.Result{RequeueAfter: stepInterval}) {
		t.Errorf("result %+v, want RequeueAfter %s", res, stepInterval)
	}
}

// TestReconcile_Upgrading_RunsStep checks that a step reaches the executor
// with the release and chart the plan names, and that the run moves on.
func TestReconcile_Upgrading_RunsStep(t *testing.T) {
	tests := []struct {
		name string
		tool kubeviltrumitev1alpha1.ToolUpgradeSpec
		want planner.UpgradeStep
	}{
		{
			name: "chart from a Helm repository; tool, chart and release named differently",
			tool: kubeviltrumitev1alpha1.ToolUpgradeSpec{
				Name: "prometheus-stack", CurrentVersion: "v0.66.0", TargetVersion: "0.94.0", Risk: ai.RiskMedium,
				Source: "helm", Namespace: "monitoring", ReleaseName: "kps",
				Chart: &kubeviltrumitev1alpha1.ChartRef{
					Name: "kube-prometheus-stack", Version: "91.0.0", Repository: "https://prometheus-community.github.io/helm-charts",
				},
			},
			want: planner.UpgradeStep{
				ToolName: "prometheus-stack", ReleaseName: "kps", Namespace: "monitoring",
				ChartRef: "kube-prometheus-stack", RepoURL: "https://prometheus-community.github.io/helm-charts",
				FromVersion: "v0.66.0", ToVersion: "91.0.0",
			},
		},
		{
			name: "chart from an OCI repository",
			tool: kubeviltrumitev1alpha1.ToolUpgradeSpec{
				Name: "cert-manager", CurrentVersion: "v1.20.4", TargetVersion: "1.21.0", Risk: ai.RiskMedium,
				Source: "helm", Namespace: "cert-manager", ReleaseName: "cert-manager",
				Chart: &kubeviltrumitev1alpha1.ChartRef{Name: "cert-manager", Version: "v1.21.0", Repository: "oci://quay.io/jetstack/charts/"},
			},
			want: planner.UpgradeStep{
				ToolName: "cert-manager", ReleaseName: "cert-manager", Namespace: "cert-manager",
				ChartRef: "oci://quay.io/jetstack/charts/cert-manager", FromVersion: "v1.20.4", ToVersion: "v1.21.0",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upgrade := newUpgrade(kubeviltrumitev1alpha1.StackUpgradeSpec{
				Approved: true,
				Tools:    []kubeviltrumitev1alpha1.ToolUpgradeSpec{tc.tool, helmTool("vault", "1.14.0", "2.0.4", "0.34.1")},
			}, kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: kubeviltrumitev1alpha1.UpgradePhaseUpgrading, TotalSteps: 2})
			r := newUpgradeReconciler(t, upgrade, interceptor.Funcs{})
			upgrader := successUpgrader()
			r.Executor = upgrader

			res := reconcileOnce(t, r, "test", "default")

			if want := []planner.UpgradeStep{tc.want}; !reflect.DeepEqual(upgrader.steps, want) {
				t.Errorf("executor steps:\n got %+v\nwant %+v", upgrader.steps, want)
			}
			result := getUpgrade(t, r, "test", "default")
			if result.Status.CurrentStep != 1 || result.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseUpgrading {
				t.Errorf("CurrentStep %d, phase %q; want 1, Upgrading", result.Status.CurrentStep, result.Status.Phase)
			}
			if c := apimeta.FindStatusCondition(result.Status.Conditions, "Progressing"); c == nil || c.Reason != "StepComplete" {
				t.Errorf("Progressing condition %+v, want reason StepComplete", c)
			}
			if res != (ctrl.Result{RequeueAfter: stepInterval}) {
				t.Errorf("result %+v, want RequeueAfter %s", res, stepInterval)
			}
		})
	}
}

// TestReconcile_Upgrading_StaleCopy checks that a reconcile which reads a
// stale copy of the plan does not run a step again. The cache can lag behind
// the status write that finished the step, and a requeue can come before the
// watch event that brings the newer copy.
func TestReconcile_Upgrading_StaleCopy(t *testing.T) {
	upgrade := newUpgrade(kubeviltrumitev1alpha1.StackUpgradeSpec{
		Approved: true,
		Tools: []kubeviltrumitev1alpha1.ToolUpgradeSpec{
			helmTool("cert-manager", "v1.20.4", "1.21.0", "v1.21.0"),
			helmTool("vault", "1.14.0", "2.0.4", "0.34.1"),
		},
	}, kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: kubeviltrumitev1alpha1.UpgradePhaseUpgrading, TotalSteps: 2})
	var stale *kubeviltrumitev1alpha1.StackUpgrade
	r := newUpgradeReconciler(t, upgrade, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if su, ok := obj.(*kubeviltrumitev1alpha1.StackUpgrade); ok && stale != nil {
				stale.DeepCopyInto(su)
				return nil
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	upgrader := successUpgrader()
	r.Executor = upgrader
	ctx := context.Background()

	// Step 1 ran and its result was written; the cache still has the copy from before.
	before := getUpgrade(t, r, "test", "default")
	done := before.DeepCopy()
	done.Status.CurrentStep = 1
	if err := r.Status().Update(ctx, done); err != nil {
		t.Fatalf("status update: %v", err)
	}
	stale = &before

	res := reconcileOnce(t, r, "test", "default")

	if len(upgrader.steps) != 0 {
		t.Errorf("executor ran %+v from the stale copy, want nothing", upgrader.steps)
	}
	if res != (ctrl.Result{RequeueAfter: stepInterval}) {
		t.Errorf("result %+v, want RequeueAfter %s", res, stepInterval)
	}

	// With the current copy, the next step runs.
	stale = nil
	reconcileOnce(t, r, "test", "default")
	if len(upgrader.steps) != 1 || upgrader.steps[0].ToolName != "vault" {
		t.Errorf("executor steps %+v, want vault's", upgrader.steps)
	}
}

// TestReconcile_Upgrading_LastStep_SetsSucceeded checks the end of the run.
func TestReconcile_Upgrading_LastStep_SetsSucceeded(t *testing.T) {
	upgrade := newUpgrade(kubeviltrumitev1alpha1.StackUpgradeSpec{
		Approved: true,
		Tools:    []kubeviltrumitev1alpha1.ToolUpgradeSpec{helmTool("cert-manager", "v1.20.4", "1.21.0", "v1.21.0")},
	}, kubeviltrumitev1alpha1.StackUpgradeStatus{
		Phase:       kubeviltrumitev1alpha1.UpgradePhaseUpgrading,
		TotalSteps:  1,
		CurrentStep: 1, // all tools already upgraded; next reconcile should mark Succeeded
	})
	r := newUpgradeReconciler(t, upgrade, interceptor.Funcs{})

	res := reconcileOnce(t, r, "test", "default")

	result := getUpgrade(t, r, "test", "default")
	if result.Status.Phase != kubeviltrumitev1alpha1.UpgradePhaseSucceeded {
		t.Errorf("Phase: got %q, want %q", result.Status.Phase, kubeviltrumitev1alpha1.UpgradePhaseSucceeded)
	}
	if result.Status.CompletedAt == nil {
		t.Error("CompletedAt: expected non-nil")
	}
	readyCond := apimeta.FindStatusCondition(result.Status.Conditions, "Ready")
	if readyCond == nil {
		t.Fatal("Ready condition not found")
	}
	if readyCond.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition status: got %q, want True", readyCond.Status)
	}
	if readyCond.Reason != "UpgradeComplete" {
		t.Errorf("Ready condition reason: got %q, want UpgradeComplete", readyCond.Reason)
	}
	if res != (ctrl.Result{}) {
		t.Errorf("result %+v, want none", res)
	}
}

// TestReconcile_FinalPhases checks that a finished plan stays finished: it is
// not reset to Pending, so an approved plan never runs a second time.
func TestReconcile_FinalPhases(t *testing.T) {
	for _, phase := range []kubeviltrumitev1alpha1.UpgradePhase{
		kubeviltrumitev1alpha1.UpgradePhaseSucceeded,
		kubeviltrumitev1alpha1.UpgradePhaseRolledBack,
	} {
		t.Run(string(phase), func(t *testing.T) {
			completed := metav1.NewTime(time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC))
			upgrade := newUpgrade(kubeviltrumitev1alpha1.StackUpgradeSpec{
				Approved: true,
				Tools:    []kubeviltrumitev1alpha1.ToolUpgradeSpec{helmTool("vault", "1.14.0", "2.0.4", "0.34.1")},
			}, kubeviltrumitev1alpha1.StackUpgradeStatus{Phase: phase, CompletedAt: &completed, TotalSteps: 1, CurrentStep: 1})
			var writes int
			r := newUpgradeReconciler(t, upgrade, interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					writes++
					return c.SubResource(sub).Update(ctx, obj, opts...)
				},
			})
			upgrader := successUpgrader()
			r.Executor = upgrader

			for range 3 {
				if res := reconcileOnce(t, r, "test", "default"); res != (ctrl.Result{}) {
					t.Errorf("result %+v, want none", res)
				}
			}

			if got := getUpgrade(t, r, "test", "default"); got.Status.Phase != phase {
				t.Errorf("phase %q, want %q", got.Status.Phase, phase)
			}
			if writes != 0 || len(upgrader.steps) != 0 {
				t.Errorf("%d status writes and steps %+v, want none", writes, upgrader.steps)
			}
		})
	}
}

package controller

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	kubeviltrumitev1alpha1 "github.com/jeikeibnaa/kube-viltrumite/api/v1alpha1"
	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
)

type fakeCluster struct {
	version      string
	installed    map[string]string
	versionErr   error
	installedErr error
}

func (f fakeCluster) KubernetesVersion(context.Context) (string, error) {
	return f.version, f.versionErr
}
func (f fakeCluster) InstalledVersions(context.Context) (map[string]string, error) {
	return f.installed, f.installedErr
}

// preflightKB has "web" 2.0.0 (needs Kubernetes 1.30, conflicts with "db" 1.x)
// and "db" 1.0.0 / 2.0.0.
func preflightKB(t *testing.T) *planner.Matrix {
	t.Helper()
	entry := func(v, min, extra string) string {
		s := "  - app_version: \"" + v + "\"\n    chart_version: \"" + v + "\"\n    source: \"https://example.com/" + v + "\"\n    min_kubernetes: \"" + min + "\"\n    risk_level: low\n    upgrade_notes: \"Read the notes.\"\n"
		return s + extra
	}
	m, err := planner.Load(fstest.MapFS{
		"web.yaml": {Data: []byte("tool: web\nchart_repository: https://charts.example.com\nversions:\n" +
			entry("1.0.0", "1.25", "") + entry("2.0.0", "1.30", "    incompatible_with:\n      db: [\"1\"]\n"))},
		"db.yaml": {Data: []byte("tool: db\nchart_repository: https://charts.example.com\nversions:\n" +
			entry("1.0.0", "1.25", "") + entry("2.0.0", "1.25", ""))},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestReconcile_Preflight(t *testing.T) {
	tests := []struct {
		name         string
		cluster      ClusterInspector
		approved     bool
		wantPhase    kubeviltrumitev1alpha1.UpgradePhase
		wantReason   string // Ready reason
		wantPreflght string // PreflightPassed reason
		wantRequeue  bool
	}{
		{
			name:         "clean plan goes on",
			cluster:      fakeCluster{version: "v1.32.2", installed: map[string]string{"db": "2.0.0"}},
			approved:     true,
			wantPhase:    kubeviltrumitev1alpha1.UpgradePhaseApproved,
			wantReason:   "Approved",
			wantPreflght: "Passed",
			wantRequeue:  true,
		},
		{
			name:         "a floor above the cluster warns and goes on",
			cluster:      fakeCluster{version: "v1.28.0", installed: map[string]string{"db": "2.0.0"}},
			approved:     true,
			wantPhase:    kubeviltrumitev1alpha1.UpgradePhaseApproved,
			wantReason:   "Approved",
			wantPreflght: "PassedWithWarnings",
			wantRequeue:  true,
		},
		{
			name:         "a conflict blocks an approved plan",
			cluster:      fakeCluster{version: "v1.32.2", installed: map[string]string{"db": "1.0.0"}},
			approved:     true,
			wantPhase:    kubeviltrumitev1alpha1.UpgradePhasePending,
			wantReason:   "PreflightBlocked",
			wantPreflght: "PreflightBlocked",
			wantRequeue:  true,
		},
		{
			name:         "a conflict shows on a plan still waiting for approval",
			cluster:      fakeCluster{version: "v1.32.2", installed: map[string]string{"db": "1.0.0"}},
			wantPhase:    kubeviltrumitev1alpha1.UpgradePhasePending,
			wantReason:   "PreflightBlocked",
			wantPreflght: "PreflightBlocked",
			wantRequeue:  true,
		},
		{
			name:         "no cluster version fails closed",
			cluster:      fakeCluster{versionErr: errors.New("connection refused")},
			approved:     true,
			wantPhase:    kubeviltrumitev1alpha1.UpgradePhasePending,
			wantReason:   "PreflightUnavailable",
			wantPreflght: "PreflightUnavailable",
			wantRequeue:  true,
		},
		{
			name:         "no installed stack fails closed",
			cluster:      fakeCluster{version: "v1.32.2", installedErr: errors.New("forbidden")},
			approved:     true,
			wantPhase:    kubeviltrumitev1alpha1.UpgradePhasePending,
			wantReason:   "PreflightUnavailable",
			wantPreflght: "PreflightUnavailable",
			wantRequeue:  true,
		},
		{
			name:       "without an inspector the checks are skipped",
			cluster:    nil,
			approved:   true,
			wantPhase:  kubeviltrumitev1alpha1.UpgradePhaseApproved,
			wantReason: "Approved", wantRequeue: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpgrade(kubeviltrumitev1alpha1.StackUpgradeSpec{
				Tools:    []kubeviltrumitev1alpha1.ToolUpgradeSpec{helmTool("web", "1.0.0", "2.0.0", "2.0.0")},
				Approved: tc.approved,
			}, kubeviltrumitev1alpha1.StackUpgradeStatus{})
			r := newUpgradeReconciler(t, up, interceptor.Funcs{})
			r.Matrix = preflightKB(t)
			r.Cluster = tc.cluster
			res := reconcileOnce(t, r, "test", "default")

			got := getUpgrade(t, r, "test", "default")
			if got.Status.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", got.Status.Phase, tc.wantPhase)
			}
			if c := apimeta.FindStatusCondition(got.Status.Conditions, "Ready"); c == nil || c.Reason != tc.wantReason {
				t.Errorf("Ready = %+v, want reason %q", c, tc.wantReason)
			}
			pc := apimeta.FindStatusCondition(got.Status.Conditions, "PreflightPassed")
			switch {
			case tc.wantPreflght == "" && pc != nil:
				t.Errorf("PreflightPassed = %+v, want none", pc)
			case tc.wantPreflght != "" && (pc == nil || pc.Reason != tc.wantPreflght):
				t.Errorf("PreflightPassed = %+v, want reason %q", pc, tc.wantPreflght)
			}
			if (res.RequeueAfter > 0) != tc.wantRequeue {
				t.Errorf("RequeueAfter = %v, want requeue %v", res.RequeueAfter, tc.wantRequeue)
			}
		})
	}
}

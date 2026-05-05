package executor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	helmkubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/jeikeibnaa/kube-viltrumite/internal/planner"
)

func newSuccessCfg(t *testing.T) *action.Configuration {
	t.Helper()
	return &action.Configuration{
		Releases:     storage.Init(driver.NewMemory()),
		KubeClient:   &helmkubefake.PrintingKubeClient{Out: io.Discard},
		Capabilities: chartutil.DefaultCapabilities,
		Log:          func(f string, v ...interface{}) { t.Logf(f, v...) },
	}
}

func newFailCfg(t *testing.T) *action.Configuration {
	t.Helper()
	return &action.Configuration{
		Releases: storage.Init(driver.NewMemory()),
		KubeClient: &helmkubefake.FailingKubeClient{
			PrintingKubeClient: helmkubefake.PrintingKubeClient{Out: io.Discard},
			BuildError:         errors.New("intentional build failure"),
		},
		Capabilities: chartutil.DefaultCapabilities,
		Log:          func(f string, v ...interface{}) { t.Logf(f, v...) },
	}
}

func seedRelease(t *testing.T, cfg *action.Configuration, name, namespace, version string, vals map[string]interface{}) {
	t.Helper()
	if vals == nil {
		vals = map[string]interface{}{}
	}
	rel := &release.Release{
		Name:      name,
		Namespace: namespace,
		Version:   1,
		Info:      &release.Info{Status: release.StatusDeployed},
		Chart: &chart.Chart{
			Metadata: &chart.Metadata{
				Name:       name,
				Version:    version,
				APIVersion: "v2",
			},
		},
		Config: vals,
	}
	if err := cfg.Releases.Create(rel); err != nil {
		t.Fatalf("seedRelease: %v", err)
	}
}

func makeTestChart(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()
	chartDir := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("makeTestChart mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(chartDir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: "+name+"\nversion: "+version+"\n"),
		0o644,
	); err != nil {
		t.Fatalf("makeTestChart Chart.yaml: %v", err)
	}
	tmpl := `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-cfg
data: {}
`
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "cm.yaml"), []byte(tmpl), 0o644); err != nil {
		t.Fatalf("makeTestChart template: %v", err)
	}
	return chartDir
}

func newExecutorWith(cfg *action.Configuration, dryRun bool) *HelmExecutor {
	e := NewHelmExecutor("", "default", dryRun)
	e.newActionConfig = func(string) (*action.Configuration, error) { return cfg, nil }
	return e
}

func TestUpgrade_DryRunSuccess(t *testing.T) {
	cfg := newSuccessCfg(t)
	seedRelease(t, cfg, "myapp", "default", "0.9.0", nil)

	e := newExecutorWith(cfg, true)
	chartDir := makeTestChart(t, "myapp", "1.0.0")

	step := planner.UpgradeStep{
		ToolName:    "myapp",
		ReleaseName: "myapp",
		ChartRef:    chartDir,
		FromVersion: "0.9.0",
		ToVersion:   "1.0.0",
	}

	result, err := e.Upgrade(context.Background(), step)
	if err != nil {
		t.Fatalf("Upgrade returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("Upgrade returned nil result")
	}
	if !result.Success {
		t.Errorf("expected Success=true; FailureReason=%q", result.FailureReason)
	}
	if result.RolledBack {
		t.Error("expected RolledBack=false for successful upgrade")
	}
}

func TestUpgrade_ReleaseNotFound(t *testing.T) {
	cfg := newSuccessCfg(t)
	e := newExecutorWith(cfg, true)

	step := planner.UpgradeStep{
		ToolName:    "ghost",
		ReleaseName: "ghost",
		ChartRef:    t.TempDir(),
		ToVersion:   "1.0.0",
	}

	_, err := e.Upgrade(context.Background(), step)
	if err == nil {
		t.Fatal("expected error for nonexistent release, got nil")
	}
	const wantPrefix = "release not found"
	if len(err.Error()) < len(wantPrefix) || err.Error()[:len(wantPrefix)] != wantPrefix {
		t.Errorf("error %q does not start with %q", err.Error(), wantPrefix)
	}
}

func TestGetCurrentValues_ReturnsMap(t *testing.T) {
	cfg := newSuccessCfg(t)
	seedRelease(t, cfg, "myapp", "default", "1.0.0", map[string]interface{}{
		"replicaCount": 3,
	})

	e := newExecutorWith(cfg, true)
	vals, err := e.GetCurrentValues(context.Background(), "myapp", "default")
	if err != nil {
		t.Fatalf("GetCurrentValues returned error: %v", err)
	}
	if vals == nil {
		t.Fatal("expected non-nil map, got nil")
	}
}

func TestUpgrade_FailureSetsRolledBack(t *testing.T) {
	cfg := newFailCfg(t)
	seedRelease(t, cfg, "myapp", "default", "0.9.0", nil)

	e := newExecutorWith(cfg, true)
	chartDir := makeTestChart(t, "myapp", "1.0.0")

	step := planner.UpgradeStep{
		ToolName:    "myapp",
		ReleaseName: "myapp",
		ChartRef:    chartDir,
		FromVersion: "0.9.0",
		ToVersion:   "1.0.0",
	}

	result, err := e.Upgrade(context.Background(), step)
	if err != nil {
		t.Fatalf("Upgrade returned unexpected Go error: %v", err)
	}
	if result == nil {
		t.Fatal("Upgrade returned nil result")
	}
	if result.Success {
		t.Error("expected Success=false for failed upgrade")
	}
	if !result.RolledBack {
		t.Error("expected RolledBack=true when atomic upgrade fails")
	}
	if result.FailureReason == "" {
		t.Error("expected non-empty FailureReason")
	}
}

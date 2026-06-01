package planner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

// knowledgeToolsDir returns the absolute path to the knowledge/tools directory
// regardless of where the test binary runs from.
func knowledgeToolsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file is .../internal/planner/matrix_test.go; go two dirs up to repo root
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "knowledge", "tools")
}

func TestLoad(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m == nil {
		t.Fatal("Load returned nil matrix")
	}
}

func TestLoad_DirNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/tools")
	if err == nil {
		t.Fatal("expected error for missing directory, got nil")
	}
}

func TestLoad_MissingToolField(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.yaml")
	if err := writeFile(bad, "versions: []"); err != nil {
		t.Fatal(err)
	}
	_, err := Load(tmp)
	if err == nil {
		t.Fatal("expected error for missing 'tool' field, got nil")
	}
}

func TestResolve(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		name                string
		tool                string
		fromVersion         string
		toVersion           string
		wantMinK8s          string
		wantRisk            string
		wantIncompatIngress []string
		wantIncompatESO     []string
		wantBreakingCount   int
	}{
		{
			name:                "1.11 to 1.12",
			tool:                "cert-manager",
			fromVersion:         "1.11",
			toVersion:           "1.12",
			wantMinK8s:          "1.22",
			wantRisk:            "medium",
			wantIncompatIngress: []string{"1.3.0", "1.3.1"},
			wantIncompatESO:     []string{},
			wantBreakingCount:   2,
		},
		{
			name:                "1.12 to 1.13",
			tool:                "cert-manager",
			fromVersion:         "1.12",
			toVersion:           "1.13",
			wantMinK8s:          "1.23",
			wantRisk:            "low",
			wantIncompatIngress: []string{"1.4.0"},
			wantIncompatESO:     []string{"0.7.0", "0.7.1"},
			wantBreakingCount:   2,
		},
		{
			name:                "1.13 to 1.14",
			tool:                "cert-manager",
			fromVersion:         "1.13",
			toVersion:           "1.14",
			wantMinK8s:          "1.23",
			wantRisk:            "low",
			wantIncompatIngress: []string{},
			wantIncompatESO:     []string{"0.8.0"},
			wantBreakingCount:   2,
		},
		{
			name:                "1.14 to 1.15",
			tool:                "cert-manager",
			fromVersion:         "1.14",
			toVersion:           "1.15",
			wantMinK8s:          "1.25",
			wantRisk:            "high",
			wantIncompatIngress: []string{"1.9.0"},
			wantIncompatESO:     []string{"0.9.0", "0.9.1"},
			wantBreakingCount:   3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := m.Resolve(tc.tool, tc.fromVersion, tc.toVersion)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			if entry.Tool != tc.tool {
				t.Errorf("Tool: got %q, want %q", entry.Tool, tc.tool)
			}
			if entry.FromVersion != tc.fromVersion {
				t.Errorf("FromVersion: got %q, want %q", entry.FromVersion, tc.fromVersion)
			}
			if entry.Version != tc.toVersion {
				t.Errorf("Version: got %q, want %q", entry.Version, tc.toVersion)
			}
			if entry.MinKubernetes != tc.wantMinK8s {
				t.Errorf("MinKubernetes: got %q, want %q", entry.MinKubernetes, tc.wantMinK8s)
			}
			if entry.RiskLevel != tc.wantRisk {
				t.Errorf("RiskLevel: got %q, want %q", entry.RiskLevel, tc.wantRisk)
			}
			if entry.UpgradeNotes == "" {
				t.Error("UpgradeNotes: must not be empty")
			}

			gotIngress := entry.IncompatibleWith["ingress-nginx"]
			if !stringSliceEqual(gotIngress, tc.wantIncompatIngress) {
				t.Errorf("IncompatibleWith[ingress-nginx]: got %v, want %v", gotIngress, tc.wantIncompatIngress)
			}

			gotESO := entry.IncompatibleWith["external-secrets"]
			if !stringSliceEqual(gotESO, tc.wantIncompatESO) {
				t.Errorf("IncompatibleWith[external-secrets]: got %v, want %v", gotESO, tc.wantIncompatESO)
			}

			if len(entry.BreakingChanges) != tc.wantBreakingCount {
				t.Errorf("BreakingChanges count: got %d, want %d", len(entry.BreakingChanges), tc.wantBreakingCount)
			}
			for i, bc := range entry.BreakingChanges {
				if bc.Description == "" {
					t.Errorf("BreakingChanges[%d].Description is empty", i)
				}
				if bc.Type == "" {
					t.Errorf("BreakingChanges[%d].Type is empty", i)
				}
			}
		})
	}
}

func TestLoad_DuplicateTool(t *testing.T) {
	tmp := t.TempDir()
	content := "tool: duplicate-tool\nversions: []\n"
	if err := writeFile(filepath.Join(tmp, "a.yaml"), content); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(tmp, "b.yaml"), content); err != nil {
		t.Fatal(err)
	}
	_, err := Load(tmp)
	if err == nil {
		t.Fatal("expected error for duplicate tool name, got nil")
	}
}

func TestResolve_UnknownTool(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = m.Resolve("velero", "1.0", "1.1")
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
}

func TestResolve_UnknownVersion(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err = m.Resolve("cert-manager", "1.15", "1.99")
	if err == nil {
		t.Fatal("expected error for unknown version, got nil")
	}
}

// TestAllToolsLoaded verifies that every expected tool YAML file loads correctly
// and meets minimum content requirements.
func TestAllToolsLoaded(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	expectedTools := []string{
		"cert-manager",
		"external-secrets",
		"argo-cd",
		"prometheus-stack",
		"istio",
		"vault",
	}

	for _, toolName := range expectedTools {
		t.Run(toolName, func(t *testing.T) {
			tc, ok := m.tools[toolName]
			if !ok {
				t.Fatalf("tool %q not found in matrix", toolName)
			}

			if len(tc.Versions) < 3 {
				t.Errorf("tool %q has %d versions, want at least 3", toolName, len(tc.Versions))
			}

			breakingTotal := 0
			for _, v := range tc.Versions {
				breakingTotal += len(v.BreakingChanges)
			}
			if breakingTotal == 0 {
				t.Errorf("tool %q has no breaking change entries across all versions", toolName)
			}
		})
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.14.0", "1.14"},
		{"1.14.2", "1.14"},
		{"1.14", "1.14"},
		{"v0.9.5", "0.9"},
		{"garbage", "garbage"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := normalizeVersion(tc.input)
			if got != tc.want {
				t.Errorf("normalizeVersion(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestResolveNormalized covers the exact production failure: raw semver strings
// from the cluster scanner ("v1.13.0", "v1.14.0") must match knowledge-base
// entries stored as major.minor ("1.14").
func TestResolveNormalized(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	entry, err := m.Resolve("cert-manager", "v1.13.0", "v1.14.0")
	if err != nil {
		t.Fatalf("Resolve with raw versions: %v", err)
	}
	if entry.FromVersion != "1.13" {
		t.Errorf("FromVersion: got %q, want %q", entry.FromVersion, "1.13")
	}
	if normalizeVersion(entry.Version) != "1.14" {
		t.Errorf("Version: got %q, want normalized 1.14", entry.Version)
	}
	if entry.MinKubernetes != "1.23" {
		t.Errorf("MinKubernetes: got %q, want %q", entry.MinKubernetes, "1.23")
	}
	if entry.RiskLevel != "low" {
		t.Errorf("RiskLevel: got %q, want %q", entry.RiskLevel, "low")
	}
}

func TestCompareMinor(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.13", "1.14", -1},
		{"1.14", "1.14", 0},
		{"1.15", "1.14", 1},
	}
	for _, tc := range tests {
		name := tc.a + "_vs_" + tc.b
		t.Run(name, func(t *testing.T) {
			got := CompareMinor(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("CompareMinor(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestListTools(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tools := m.ListTools()
	found := false
	for _, n := range tools {
		if n == "cert-manager" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListTools: expected cert-manager to be present")
	}

	for i := 1; i < len(tools); i++ {
		if tools[i] < tools[i-1] {
			t.Errorf("ListTools: result not sorted: %q before %q", tools[i-1], tools[i])
		}
	}
}

func TestLatestSafeVersion(t *testing.T) {
	m, err := Load(knowledgeToolsDir(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tests := []struct {
		name        string
		tool        string
		current     string
		tolerance   ai.RiskLevel
		wantVersion string
		wantRisk    ai.RiskLevel
		wantFound   bool
	}{
		{
			// 1.14 is low, 1.15 is high — only 1.14 qualifies at MEDIUM ceiling
			name:        "medium tolerance above 1.13 finds 1.14",
			tool:        "cert-manager",
			current:     "1.13",
			tolerance:   ai.RiskMedium,
			wantVersion: "1.14",
			wantRisk:    ai.RiskLow,
			wantFound:   true,
		},
		{
			// 1.15 is the last entry; nothing above it
			name:      "low tolerance above 1.15 finds nothing",
			tool:      "cert-manager",
			current:   "1.15",
			tolerance: ai.RiskLow,
			wantFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, risk, found := m.LatestSafeVersion(tc.tool, tc.current, tc.tolerance)
			if found != tc.wantFound {
				t.Fatalf("found=%v, want %v", found, tc.wantFound)
			}
			if !found {
				return
			}
			if rec != tc.wantVersion {
				t.Errorf("recommended=%q, want %q", rec, tc.wantVersion)
			}
			if risk != tc.wantRisk {
				t.Errorf("risk=%q, want %q", risk, tc.wantRisk)
			}
		})
	}
}

func TestRiskAtOrBelow(t *testing.T) {
	tests := []struct {
		risk    ai.RiskLevel
		ceiling ai.RiskLevel
		want    bool
	}{
		{ai.RiskLow, ai.RiskMedium, true},
		{ai.RiskHigh, ai.RiskLow, false},
		{ai.RiskLow, ai.RiskLow, true},
		{ai.RiskMedium, ai.RiskMedium, true},
		{ai.RiskBlocking, ai.RiskHigh, false},
	}
	for _, tc := range tests {
		name := string(tc.risk) + "_leq_" + string(tc.ceiling)
		t.Run(name, func(t *testing.T) {
			got := RiskAtOrBelow(tc.risk, tc.ceiling)
			if got != tc.want {
				t.Errorf("RiskAtOrBelow(%q, %q) = %v, want %v", tc.risk, tc.ceiling, got, tc.want)
			}
		})
	}
}

// stringSliceEqual compares two string slices treating nil and empty as equal.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeFile writes content to path (helper for negative-case tests).
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

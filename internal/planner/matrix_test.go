package planner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
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

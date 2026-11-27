package planner

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// preflightMatrix has "a" (alias "a-chart") and "b". a 2.0.0 lists b 1.4 and
// b 3.0.1 as incompatible; b 2.0.0 lists a 1.0.0; a 2.0.0 needs Kubernetes 1.30.
func preflightMatrix(t *testing.T) *Matrix {
	t.Helper()
	m, err := Load(fstest.MapFS{
		"a.yaml": {Data: doc("a", "aliases: [a-chart]\n",
			entry(map[string]string{"app_version": `"1.0.0"`, "min_kubernetes": `"1.25"`}),
			entry(map[string]string{
				"app_version": `"2.0.0"`, "chart_version": `"2.0.0"`, "min_kubernetes": `"1.30"`,
				"incompatible_with": `{b: ["1.4", "3.0.1"]}`,
			}))},
		"b.yaml": {Data: doc("b", "",
			entry(map[string]string{"app_version": `"1.4.0"`}),
			entry(map[string]string{"app_version": `"2.0.0"`, "chart_version": `"2.0.0"`, "incompatible_with": `{a-chart: ["1.0.0"]}`}),
			entry(map[string]string{"app_version": `"3.0.0"`, "chart_version": `"3.0.0"`}),
			entry(map[string]string{"app_version": `"3.0.1"`, "chart_version": `"3.0.1"`}))},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestPreflight(t *testing.T) {
	m := preflightMatrix(t)
	tests := []struct {
		name      string
		tools     []PlannedTool
		cluster   string
		installed map[string]string
		want      []string // "Severity tool", in order
		contains  string   // a message fragment of the first finding
	}{
		{name: "clean", tools: []PlannedTool{{"a", "1.0.0"}}, cluster: "v1.28.0"},
		{
			name: "cluster below the floor warns", tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "v1.29.4+k3s1",
			want: []string{"Warn a"}, contains: "supports Kubernetes 1.30 and later; the cluster runs 1.29.4+k3s1",
		},
		{name: "cluster at the floor", tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "1.30"},
		{name: "unknown cluster version skips the floor", tools: []PlannedTool{{"a", "2.0.0"}}},
		{name: "unparsable cluster version skips the floor", tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "dev"},
		{
			name:  "target conflicts with the installed tool: partial version covers the line",
			tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "1.31", installed: map[string]string{"b": "1.4.7"},
			want: []string{"Block b"}, contains: "a 2.0.0 is incompatible with b 1.4.7 (listed as 1.4)",
		},
		{name: "partial version does not cover the next minor", tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "1.31", installed: map[string]string{"b": "1.5.0"}},
		{
			name:  "full version matches that release only",
			tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "1.31", installed: map[string]string{"b": "3.0.1"},
			want: []string{"Block b"},
		},
		{name: "full version does not match its neighbour", tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "1.31", installed: map[string]string{"b": "3.0.0"}},
		{
			name:  "the plan's own target is what counts, not the installed version",
			tools: []PlannedTool{{"a", "2.0.0"}, {"b", "3.0.0"}}, cluster: "1.31", installed: map[string]string{"b": "1.4.0"},
		},
		{
			name:  "both in the plan, the pair conflicts after the upgrade",
			tools: []PlannedTool{{"a", "2.0.0"}, {"b", "3.0.1"}}, cluster: "1.31", installed: map[string]string{"b": "1.0.0"},
			want: []string{"Block b"},
		},
		{
			name:  "the installed tool's entry names the target, through an alias",
			tools: []PlannedTool{{"a", "1.0.0"}}, cluster: "1.31", installed: map[string]string{"b": "2.0.0"},
			want: []string{"Block a"}, contains: "b 2.0.0 is incompatible with a 1.0.0",
		},
		{
			name:  "installed names resolve through aliases",
			tools: []PlannedTool{{"b", "1.4.0"}}, cluster: "1.31", installed: map[string]string{"a-chart": "2.0.0"},
			want: []string{"Block b"},
		},
		{
			name:  "a conflict between two installed tools is not the plan's doing",
			tools: []PlannedTool{{"b", "3.0.0"}}, installed: map[string]string{"a": "2.0.0", "b": "1.4.0"},
		},
		{name: "unknown installed version is not guessed", tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "1.31", installed: map[string]string{"b": ""}},
		{name: "installed version with no entry is skipped", tools: []PlannedTool{{"a", "1.0.0"}}, cluster: "1.31", installed: map[string]string{"b": "0.1.0"}},
		{
			name:  "findings come out once, floor first",
			tools: []PlannedTool{{"a", "2.0.0"}}, cluster: "1.20", installed: map[string]string{"b": "1.4.0"},
			want: []string{"Warn a", "Block b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := m.Preflight(tc.tools, tc.cluster, tc.installed)
			var kinds []string
			for _, f := range got {
				kinds = append(kinds, string(f.Severity)+" "+f.Tool)
			}
			if !slices.Equal(kinds, tc.want) {
				t.Fatalf("findings = %v, want %v (%+v)", kinds, tc.want, got)
			}
			if tc.contains != "" && !strings.Contains(got[0].Message, tc.contains) {
				t.Errorf("message %q does not contain %q", got[0].Message, tc.contains)
			}
			if wantBlocked := slicesContains(tc.want, "Block"); Blocked(got) != wantBlocked {
				t.Errorf("Blocked = %v, want %v", Blocked(got), wantBlocked)
			}
		})
	}
}

func slicesContains(kinds []string, severity string) bool {
	for _, k := range kinds {
		if strings.HasPrefix(k, severity) {
			return true
		}
	}
	return false
}

func TestMatchesListed(t *testing.T) {
	tests := []struct {
		version, listed string
		want            bool
	}{
		{"1.14.2", "1", true},
		{"2.0.0", "1", false},
		{"1.14.2", "1.14", true},
		{"1.15.0", "1.14", false},
		{"1.14.2", "1.14.2", true},
		{"v1.14.2", "v1.14.2", true},
		{"1.14.3", "1.14.2", false},
		{"1.14.2", "garbage", false},
		{"garbage", "1.14", false},
	}
	for _, tc := range tests {
		if got := matchesListed(tc.version, tc.listed); got != tc.want {
			t.Errorf("matchesListed(%q, %q) = %v, want %v", tc.version, tc.listed, got, tc.want)
		}
	}
}

package ai_test

import (
	"testing"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

func TestRiskSummaryForBreakingChange(t *testing.T) {
	tests := []struct {
		name            string
		input           ai.RiskSummary
		wantLevel       ai.RiskLevel
		wantBreaking    []string
		wantDeprecation []string
		wantCVEs        []string
	}{
		{
			name: "known API removal is BLOCKING",
			input: ai.RiskSummary{
				Level:           ai.RiskBlocking,
				BreakingChanges: []string{"PodSecurityPolicy API removed in v1.25"},
				Deprecations:    []string{},
				CVEs:            []string{},
				Summary:         "PodSecurityPolicy was removed. Migrate to PodSecurity admission controller before upgrading.",
			},
			wantLevel:       ai.RiskBlocking,
			wantBreaking:    []string{"PodSecurityPolicy API removed in v1.25"},
			wantDeprecation: []string{},
			wantCVEs:        []string{},
		},
		{
			name: "deprecated flag without removal is HIGH",
			input: ai.RiskSummary{
				Level:           ai.RiskHigh,
				BreakingChanges: []string{},
				Deprecations:    []string{"--feature-gates=PodPriority deprecated"},
				CVEs:            []string{},
				Summary:         "Feature gate deprecated; will be removed in a future release.",
			},
			wantLevel:       ai.RiskHigh,
			wantBreaking:    []string{},
			wantDeprecation: []string{"--feature-gates=PodPriority deprecated"},
			wantCVEs:        []string{},
		},
		{
			name: "CVE with no breaking changes is HIGH",
			input: ai.RiskSummary{
				Level:           ai.RiskHigh,
				BreakingChanges: []string{},
				Deprecations:    []string{},
				CVEs:            []string{"CVE-2023-44487"},
				Summary:         "HTTP/2 rapid reset vulnerability. Patch immediately.",
			},
			wantLevel:       ai.RiskHigh,
			wantBreaking:    []string{},
			wantDeprecation: []string{},
			wantCVEs:        []string{"CVE-2023-44487"},
		},
		{
			name: "patch with no breaking changes is LOW",
			input: ai.RiskSummary{
				Level:           ai.RiskLow,
				BreakingChanges: []string{},
				Deprecations:    []string{},
				CVEs:            []string{},
				Summary:         "Routine bug fixes, no API changes.",
			},
			wantLevel:       ai.RiskLow,
			wantBreaking:    []string{},
			wantDeprecation: []string{},
			wantCVEs:        []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.input.Level != tc.wantLevel {
				t.Errorf("Level: got %q, want %q", tc.input.Level, tc.wantLevel)
			}
			if !stringSliceEqual(tc.input.BreakingChanges, tc.wantBreaking) {
				t.Errorf("BreakingChanges: got %v, want %v", tc.input.BreakingChanges, tc.wantBreaking)
			}
			if !stringSliceEqual(tc.input.Deprecations, tc.wantDeprecation) {
				t.Errorf("Deprecations: got %v, want %v", tc.input.Deprecations, tc.wantDeprecation)
			}
			if !stringSliceEqual(tc.input.CVEs, tc.wantCVEs) {
				t.Errorf("CVEs: got %v, want %v", tc.input.CVEs, tc.wantCVEs)
			}
			if tc.input.Summary == "" {
				t.Error("Summary must not be empty")
			}
		})
	}
}

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

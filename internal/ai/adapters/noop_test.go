package adapters_test

import (
	"context"
	"testing"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
	"github.com/jeikeibnaa/kube-viltrumite/internal/ai/adapters"
)

func TestNewNoopProvider_satisfiesInterface(t *testing.T) {
	var _ ai.AIProvider = adapters.NewNoopProvider()
}

func TestNoopProvider_IsAvailable(t *testing.T) {
	p := adapters.NewNoopProvider()
	if p.IsAvailable(context.Background()) {
		t.Error("IsAvailable: got true, want false")
	}
}

func TestNoopProvider_AnalyzeChangelog(t *testing.T) {
	p := adapters.NewNoopProvider()
	req := ai.ChangelogRequest{
		ToolName:      "kubernetes",
		FromVersion:   "1.27.0",
		ToVersion:     "1.28.0",
		ChangelogText: "some changelog",
	}

	got, err := p.AnalyzeChangelog(context.Background(), req)
	if err != nil {
		t.Fatalf("AnalyzeChangelog returned unexpected error: %v", err)
	}
	if got.Level != ai.RiskLow {
		t.Errorf("Level: got %q, want %q", got.Level, ai.RiskLow)
	}
	if got.Summary != "AI analysis not configured" {
		t.Errorf("Summary: got %q, want %q", got.Summary, "AI analysis not configured")
	}
	if len(got.BreakingChanges) != 0 {
		t.Errorf("BreakingChanges: got %v, want empty", got.BreakingChanges)
	}
	if len(got.Deprecations) != 0 {
		t.Errorf("Deprecations: got %v, want empty", got.Deprecations)
	}
	if len(got.CVEs) != 0 {
		t.Errorf("CVEs: got %v, want empty", got.CVEs)
	}
}

func TestNoopProvider_ExplainUpgrade(t *testing.T) {
	p := adapters.NewNoopProvider()
	plan := &ai.UpgradePlan{
		Steps:     []ai.UpgradeStep{},
		TotalRisk: ai.RiskLow,
	}

	got, err := p.ExplainUpgrade(context.Background(), plan)
	if err != nil {
		t.Fatalf("ExplainUpgrade returned unexpected error: %v", err)
	}
	if got != "AI explanation not configured" {
		t.Errorf("ExplainUpgrade: got %q, want %q", got, "AI explanation not configured")
	}
}

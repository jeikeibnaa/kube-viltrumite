package adapters

import (
	"context"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

type NoopProvider struct{}

func NewNoopProvider() ai.AIProvider {
	return &NoopProvider{}
}

func (n *NoopProvider) AnalyzeChangelog(_ context.Context, _ ai.ChangelogRequest) (*ai.RiskSummary, error) {
	return &ai.RiskSummary{
		Level:           ai.RiskLow,
		BreakingChanges: []string{},
		Deprecations:    []string{},
		CVEs:            []string{},
		Summary:         "AI analysis not configured",
	}, nil
}

func (n *NoopProvider) ExplainUpgrade(_ context.Context, _ *ai.UpgradePlan) (string, error) {
	return "AI explanation not configured", nil
}

func (n *NoopProvider) IsAvailable(_ context.Context) bool {
	return false
}

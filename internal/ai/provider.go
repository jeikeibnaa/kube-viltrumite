package ai

import "context"

type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskBlocking RiskLevel = "BLOCKING"
)

type ChangelogRequest struct {
	ToolName      string
	FromVersion   string
	ToVersion     string
	ChangelogText string
}

type RiskSummary struct {
	Level           RiskLevel
	BreakingChanges []string
	Deprecations    []string
	CVEs            []string
	Summary         string
}

type UpgradeStep struct {
	ToolName    string
	FromVersion string
	ToVersion   string
	Risk        RiskLevel
	Reason      string
	PreChecks   []string
	PostChecks  []string
}

type UpgradePlan struct {
	Steps     []UpgradeStep
	TotalRisk RiskLevel
}

type AIProvider interface {
	AnalyzeChangelog(ctx context.Context, req ChangelogRequest) (*RiskSummary, error)
	ExplainUpgrade(ctx context.Context, plan *UpgradePlan) (string, error)
	IsAvailable(ctx context.Context) bool
}

package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

type OllamaConfig struct {
	Endpoint string
	Model    string
	Timeout  time.Duration
}

type OllamaProvider struct {
	cfg    OllamaConfig
	client *http.Client
}

func NewOllamaProvider(cfg OllamaConfig) ai.AIProvider {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &OllamaProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
}

func (o *OllamaProvider) chat(ctx context.Context, prompt string) (string, error) {
	body := ollamaChatRequest{
		Model:    o.cfg.Model,
		Messages: []ollamaMessage{{Role: "user", Content: prompt}},
		Stream:   false,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.Endpoint+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama chat: unexpected status %d", resp.StatusCode)
	}

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("ollama chat: decode response: %w", err)
	}
	return result.Message.Content, nil
}

func (o *OllamaProvider) AnalyzeChangelog(ctx context.Context, req ai.ChangelogRequest) (*ai.RiskSummary, error) {
	prompt := fmt.Sprintf(`You are a Kubernetes upgrade risk analyzer. Analyze the following changelog and respond with ONLY valid JSON — no explanation, no markdown, no extra text.

The JSON must exactly match this schema:
{
  "Level": "<LOW|MEDIUM|HIGH|BLOCKING>",
  "BreakingChanges": ["<string>", ...],
  "Deprecations": ["<string>", ...],
  "CVEs": ["<string>", ...],
  "Summary": "<one-paragraph summary>"
}

Tool: %s
From version: %s
To version: %s

Changelog:
%s`, req.ToolName, req.FromVersion, req.ToVersion, req.ChangelogText)

	content, err := o.chat(ctx, prompt)
	if err != nil {
		return nil, err
	}

	var summary ai.RiskSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		return nil, fmt.Errorf("ollama: parse risk summary JSON: %w", err)
	}
	return &summary, nil
}

func (o *OllamaProvider) ExplainUpgrade(ctx context.Context, plan *ai.UpgradePlan) (string, error) {
	stepsJSON, err := json.Marshal(plan.Steps)
	if err != nil {
		return "", err
	}

	prompt := fmt.Sprintf(`You are a Kubernetes upgrade advisor. Explain the following upgrade plan in plain English.
Describe the overall risk (%s) and what each step involves, including any pre/post checks.

Upgrade steps:
%s`, plan.TotalRisk, string(stepsJSON))

	return o.chat(ctx, prompt)
}

func (o *OllamaProvider) IsAvailable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.cfg.Endpoint+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

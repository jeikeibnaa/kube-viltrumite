package adapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

func ollamaReply(t *testing.T, content string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaChatResponse{
			Message: ollamaMessage{Role: "assistant", Content: content},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func TestOllamaProvider_IsAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{Endpoint: srv.URL, Model: "llama3", Timeout: 5 * time.Second})
	if !p.IsAvailable(context.Background()) {
		t.Fatal("expected IsAvailable to return true")
	}
}

func TestOllamaProvider_IsAvailable_ServerDown(t *testing.T) {
	p := NewOllamaProvider(OllamaConfig{Endpoint: "http://127.0.0.1:1", Model: "llama3", Timeout: time.Second})
	if p.IsAvailable(context.Background()) {
		t.Fatal("expected IsAvailable to return false for unreachable server")
	}
}

func TestOllamaProvider_AnalyzeChangelog(t *testing.T) {
	want := ai.RiskSummary{
		Level:           ai.RiskHigh,
		BreakingChanges: []string{"API v1beta1 removed"},
		Deprecations:    []string{"deprecated flag --foo"},
		CVEs:            []string{"CVE-2024-1234"},
		Summary:         "Significant breaking changes in this release.",
	}
	rawJSON, _ := json.Marshal(want)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat" && r.Method == http.MethodPost {
			ollamaReply(t, string(rawJSON))(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{Endpoint: srv.URL, Model: "llama3", Timeout: 5 * time.Second})
	got, err := p.AnalyzeChangelog(context.Background(), ai.ChangelogRequest{
		ToolName:      "etcd",
		FromVersion:   "3.4.0",
		ToVersion:     "3.5.0",
		ChangelogText: "## Breaking changes\n- API v1beta1 removed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Level != want.Level {
		t.Errorf("Level: got %q, want %q", got.Level, want.Level)
	}
	if len(got.BreakingChanges) != 1 || got.BreakingChanges[0] != want.BreakingChanges[0] {
		t.Errorf("BreakingChanges mismatch: got %v", got.BreakingChanges)
	}
	if got.Summary != want.Summary {
		t.Errorf("Summary: got %q, want %q", got.Summary, want.Summary)
	}
}

func TestOllamaProvider_AnalyzeChangelog_BadJSON(t *testing.T) {
	srv := httptest.NewServer(ollamaReply(t, "not json at all"))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{Endpoint: srv.URL, Model: "llama3", Timeout: 5 * time.Second})
	_, err := p.AnalyzeChangelog(context.Background(), ai.ChangelogRequest{ToolName: "k8s"})
	if err == nil {
		t.Fatal("expected error for bad JSON response")
	}
}

func TestOllamaProvider_AnalyzeChangelog_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{Endpoint: srv.URL, Model: "llama3", Timeout: 5 * time.Second})
	_, err := p.AnalyzeChangelog(context.Background(), ai.ChangelogRequest{ToolName: "k8s"})
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestOllamaProvider_ExplainUpgrade(t *testing.T) {
	const explanation = "This upgrade is straightforward."
	srv := httptest.NewServer(ollamaReply(t, explanation))
	defer srv.Close()

	p := NewOllamaProvider(OllamaConfig{Endpoint: srv.URL, Model: "llama3", Timeout: 5 * time.Second})
	plan := &ai.UpgradePlan{
		TotalRisk: ai.RiskMedium,
		Steps: []ai.UpgradeStep{
			{ToolName: "etcd", FromVersion: "3.4", ToVersion: "3.5", Risk: ai.RiskMedium},
		},
	}
	got, err := p.ExplainUpgrade(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != explanation {
		t.Errorf("got %q, want %q", got, explanation)
	}
}

func TestNewOllamaProvider_DefaultTimeout(t *testing.T) {
	p := NewOllamaProvider(OllamaConfig{Endpoint: "http://localhost:11434", Model: "llama3"})
	op := p.(*OllamaProvider)
	if op.client.Timeout != 30*time.Second {
		t.Errorf("expected default timeout 30s, got %v", op.client.Timeout)
	}
}

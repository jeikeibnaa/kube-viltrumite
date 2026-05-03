package adapters

import (
	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

type OpenAIConfig struct {
	Endpoint string
	APIKey   string
	Model    string
}

func NewOpenAIProvider(cfg OpenAIConfig) ai.AIProvider {
	return NewNoopProvider()
}

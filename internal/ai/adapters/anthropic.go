package adapters

import (
	"github.com/jeikeibnaa/kube-viltrumite/internal/ai"
)

type AnthropicConfig struct {
	APIKey string
	Model  string
}

func NewAnthropicProvider(cfg AnthropicConfig) ai.AIProvider {
	return NewNoopProvider()
}

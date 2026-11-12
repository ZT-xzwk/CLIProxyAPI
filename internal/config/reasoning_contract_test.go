package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAICompatibilityReasoningContractYAML(t *testing.T) {
	const yamlConfig = `openai-compatibility:
  - name: provider
    models:
      - name: deepseek-upstream
        alias: deepseek-alias
        reasoning-contract: deepseek
      - name: openrouter-upstream
        alias: openrouter-alias
        reasoning-contract: openrouter
      - name: default-upstream
        alias: default-alias
`
	var cfg Config
	if errUnmarshal := yaml.Unmarshal([]byte(yamlConfig), &cfg); errUnmarshal != nil {
		t.Fatalf("yaml.Unmarshal error = %v", errUnmarshal)
	}
	if len(cfg.OpenAICompatibility) != 1 || len(cfg.OpenAICompatibility[0].Models) != 3 {
		t.Fatalf("unexpected models decoded: %+v", cfg.OpenAICompatibility)
	}
	models := cfg.OpenAICompatibility[0].Models
	if models[0].ReasoningContract != "deepseek" {
		t.Fatalf("deepseek model ReasoningContract = %q, want deepseek", models[0].ReasoningContract)
	}
	if models[1].ReasoningContract != "openrouter" {
		t.Fatalf("openrouter model ReasoningContract = %q, want openrouter", models[1].ReasoningContract)
	}
	if models[2].ReasoningContract != "" {
		t.Fatalf("default model ReasoningContract = %q, want empty", models[2].ReasoningContract)
	}
}

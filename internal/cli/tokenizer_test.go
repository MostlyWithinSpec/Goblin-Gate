package cli

import (
	"testing"
)

func TestTokenizer(t *testing.T) {
	// Just verify it compiles and runs without errors
	tokenizer := NewTokenizer("config firewall policy")
	tokens := tokenizer.Tokens()

	// Should have at least 3 tokens (config, firewall, policy) + EOF
	if len(tokens) < 4 {
		t.Errorf("Expected at least 4 tokens, got %d", len(tokens))
	}

	// First token should be config
	if tokens[0].Type != TokenConfig {
		t.Errorf("First token should be TokenConfig, got %v", tokens[0].Type)
	}

	// Second token should be firewall
	if tokens[1].Type != TokenString || tokens[1].Value != "firewall" {
		t.Errorf("Second token should be 'firewall', got %v", tokens[1])
	}
}

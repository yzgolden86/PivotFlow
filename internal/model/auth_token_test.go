package model

import (
	"testing"
)

func TestIsProtocolAllowed(t *testing.T) {
	tests := []struct {
		name             string
		allowedProtocols []string
		protocol         string
		expected         bool
	}{
		{
			name:             "empty list allows all protocols",
			allowedProtocols: []string{},
			protocol:         "anthropic",
			expected:         true,
		},
		{
			name:             "nil list allows all protocols",
			allowedProtocols: nil,
			protocol:         "openai",
			expected:         true,
		},
		{
			name:             "exact match case insensitive",
			allowedProtocols: []string{"anthropic", "openai"},
			protocol:         "ANTHROPIC",
			expected:         true,
		},
		{
			name:             "protocol not in list",
			allowedProtocols: []string{"anthropic", "openai"},
			protocol:         "gemini",
			expected:         false,
		},
		{
			name:             "whitespace handling",
			allowedProtocols: []string{"  anthropic  ", "openai"},
			protocol:         "  ANTHROPIC  ",
			expected:         true,
		},
		{
			name:             "single protocol allowed",
			allowedProtocols: []string{"codex"},
			protocol:         "codex",
			expected:         true,
		},
		{
			name:             "single protocol denied",
			allowedProtocols: []string{"codex"},
			protocol:         "anthropic",
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &AuthToken{
				AllowedProtocols: tt.allowedProtocols,
			}
			result := token.IsProtocolAllowed(tt.protocol)
			if result != tt.expected {
				t.Errorf("IsProtocolAllowed(%q) = %v, expected %v", tt.protocol, result, tt.expected)
			}
		})
	}
}

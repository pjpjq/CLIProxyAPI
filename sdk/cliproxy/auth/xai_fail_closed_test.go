package auth

import "testing"

func TestApplyXAIFailClosedProxy(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		metadata    map[string]any
		proxyURL    string
		environment string
		expected    string
	}{
		{
			name:     "empty xai uses default",
			provider: "xai",
			metadata: map[string]any{"type": "xai"},
			expected: defaultXAIFailClosedProxyURL,
		},
		{
			name:        "direct xai uses environment override",
			provider:    "xai",
			metadata:    map[string]any{"type": "xai"},
			proxyURL:    "direct",
			environment: "socks5h://127.0.0.1:1999",
			expected:    "socks5h://127.0.0.1:1999",
		},
		{
			name:        "direct environment override disables proxy for tests",
			provider:    "xai",
			metadata:    map[string]any{"type": "xai"},
			environment: "direct",
			expected:    "direct",
		},
		{
			name:     "none xai uses default",
			provider: "xai",
			metadata: map[string]any{"type": "xai"},
			proxyURL: "none",
			expected: defaultXAIFailClosedProxyURL,
		},
		{
			name:        "invalid proxy and invalid override use default",
			provider:    "xai",
			metadata:    map[string]any{"type": "xai"},
			proxyURL:    "not-a-proxy",
			environment: "also-not-a-proxy",
			expected:    defaultXAIFailClosedProxyURL,
		},
		{
			name:     "existing xai proxy is preserved",
			provider: "xai",
			metadata: map[string]any{"type": "xai"},
			proxyURL: "socks5h://127.0.0.1:1080",
			expected: "socks5h://127.0.0.1:1080",
		},
		{
			name:     "metadata proxy is preserved",
			provider: "xai",
			metadata: map[string]any{
				"type":      "xai",
				"proxy_url": "socks5h://127.0.0.1:1081",
			},
			expected: "socks5h://127.0.0.1:1081",
		},
		{
			name:     "non xai is unchanged",
			provider: "codex",
			metadata: map[string]any{"type": "codex"},
			expected: "",
		},
		{
			name:     "provider only xai is unchanged",
			provider: "xai",
			metadata: map[string]any{"type": "codex"},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XAI_PROXY_FAIL_CLOSED_URL", test.environment)
			auth := &Auth{
				Provider: test.provider,
				Metadata: test.metadata,
				ProxyURL: test.proxyURL,
			}
			applyXAIFailClosedProxy(auth)
			if auth.ProxyURL != test.expected {
				t.Fatalf("ProxyURL = %q, want %q", auth.ProxyURL, test.expected)
			}
			if test.expected != "" && auth.Metadata["proxy_url"] != test.expected {
				t.Fatalf("metadata proxy_url = %v, want %q", auth.Metadata["proxy_url"], test.expected)
			}
		})
	}
}

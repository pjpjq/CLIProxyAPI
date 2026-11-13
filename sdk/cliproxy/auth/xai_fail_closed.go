package auth

import (
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/proxyutil"
)

const defaultXAIFailClosedProxyURL = "socks5h://127.0.0.1:9"

func concreteProxyURL(raw string) bool {
	setting, err := proxyutil.Parse(strings.TrimSpace(raw))
	return err == nil && setting.Mode == proxyutil.ModeProxy
}

func applyXAIFailClosedProxy(auth *Auth) {
	if auth == nil {
		return
	}
	provider := strings.TrimSpace(auth.Provider)
	metadataType := ""
	if auth.Metadata != nil {
		metadataType, _ = auth.Metadata["type"].(string)
	}
	if !strings.EqualFold(provider, "xai") || !strings.EqualFold(strings.TrimSpace(metadataType), "xai") {
		return
	}

	proxyURL := strings.TrimSpace(auth.ProxyURL)
	if proxyURL == "" && auth.Metadata != nil {
		if raw, ok := auth.Metadata["proxy_url"].(string); ok {
			proxyURL = strings.TrimSpace(raw)
			auth.ProxyURL = proxyURL
		}
	}
	if concreteProxyURL(proxyURL) {
		auth.ProxyURL = proxyURL
		if auth.Metadata == nil {
			auth.Metadata = make(map[string]any)
		}
		auth.Metadata["proxy_url"] = proxyURL
		return
	}

	failClosed := strings.TrimSpace(os.Getenv("XAI_PROXY_FAIL_CLOSED_URL"))
	if strings.EqualFold(failClosed, "direct") || strings.EqualFold(failClosed, "none") {
		auth.ProxyURL = strings.ToLower(failClosed)
		if auth.Metadata == nil {
			auth.Metadata = make(map[string]any)
		}
		auth.Metadata["proxy_url"] = strings.ToLower(failClosed)
		return
	}
	if !concreteProxyURL(failClosed) {
		failClosed = defaultXAIFailClosedProxyURL
	}
	auth.ProxyURL = failClosed
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["proxy_url"] = failClosed
}

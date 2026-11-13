package auth

import (
	"maps"
	"strings"
)

// ListModelRefreshCandidates returns detached model-registration inputs for enabled
// auths of one provider. It omits execution state (quota, model states and runtime);
// use List or GetByID when a complete auth snapshot is required.
func (m *Manager) ListModelRefreshCandidates(provider string) []*Auth {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var candidates []*Auth
	for _, a := range m.auths {
		if a == nil || a.Disabled || !strings.EqualFold(a.Provider, provider) {
			continue
		}
		candidates = append(candidates, &Auth{
			ID: a.ID, RegistrationEpoch: a.RegistrationEpoch,
			Provider: a.Provider, Prefix: a.Prefix, Status: a.Status,
			ProxyURL:   a.ProxyURL,
			Attributes: maps.Clone(a.Attributes), Metadata: maps.Clone(a.Metadata),
		})
	}
	return candidates
}

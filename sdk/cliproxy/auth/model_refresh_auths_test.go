package auth

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestListModelRefreshCandidates(t *testing.T) {
	a := &Auth{
		ID: "candidate", RegistrationEpoch: 7, Provider: "Antigravity",
		Prefix: "team", Status: StatusActive, ProxyURL: "http://proxy.invalid",
		Attributes:  map[string]string{"base_url": "http://catalog.invalid", "excluded_models": "excluded"},
		Metadata:    map[string]any{"access_token": "test-token", "project_id": "project"},
		ModelStates: map[string]*ModelState{"model": {Unavailable: true}},
		Quota:       QuotaState{Exceeded: true}, Runtime: struct{}{},
	}
	m := &Manager{auths: map[string]*Auth{
		a.ID: a, "nil": nil,
		"other":           {ID: "other", Provider: "codex"},
		"disabled":        {ID: "disabled", Provider: "antigravity", Disabled: true},
		"status-disabled": {ID: "status-disabled", Provider: "antigravity", Status: StatusDisabled},
	}}
	got := m.ListModelRefreshCandidates("antigravity")
	if len(got) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(got))
	}
	var snapshot *Auth
	for _, candidate := range got {
		if candidate.ID == a.ID {
			snapshot = candidate
		} else if candidate.ID != "status-disabled" || candidate.Status != StatusDisabled {
			t.Fatal("filter or status changed")
		}
	}
	want := &Auth{
		ID: a.ID, RegistrationEpoch: a.RegistrationEpoch, Provider: a.Provider,
		Prefix: a.Prefix, Status: a.Status, ProxyURL: a.ProxyURL,
		Attributes: a.Attributes, Metadata: a.Metadata,
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatal("model-registration inputs changed or execution state was copied")
	}
	snapshot.Attributes["base_url"] = "changed"
	snapshot.Metadata["access_token"] = "changed"
	snapshot.Prefix = "changed"
	if a.Attributes["base_url"] != "http://catalog.invalid" || a.Metadata["access_token"] != "test-token" || a.Prefix != "team" {
		t.Fatal("snapshot mutation reached manager state")
	}
	a.Metadata["project_id"] = "updated"
	if snapshot.Metadata["project_id"] != "project" {
		t.Fatal("manager mutation reached snapshot")
	}
	if len(m.ListModelRefreshCandidates("missing")) != 0 || (*Manager)(nil).ListModelRefreshCandidates("antigravity") != nil {
		t.Fatal("empty lookup must return no candidates")
	}
	// Keep nil metadata/attributes nil, matching Clone's map semantics.
	m.auths = map[string]*Auth{"empty": {Provider: "antigravity"}}
	if empty := m.ListModelRefreshCandidates("antigravity")[0]; empty.Attributes != nil || empty.Metadata != nil {
		t.Fatal("nil maps changed")
	}
}

func TestListModelRefreshCandidatesConcurrentMutation(t *testing.T) {
	m := &Manager{auths: map[string]*Auth{"account": {
		ID: "account", Provider: "antigravity",
		Attributes: map[string]string{"base_url": "initial"},
		Metadata:   map[string]any{"access_token": "initial"},
	}}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 500 {
			m.mu.Lock()
			a := m.auths["account"]
			value := fmt.Sprint(i)
			a.Attributes["base_url"] = value
			a.Metadata["access_token"] = value
			a.Disabled = i%3 == 0
			m.mu.Unlock()
		}
	}()
	for range 500 {
		for _, a := range m.ListModelRefreshCandidates("antigravity") {
			if a.Attributes["base_url"] != a.Metadata["access_token"] {
				t.Error("incoherent snapshot")
			}
			a.Attributes["base_url"] = "local-only"
			a.Metadata["access_token"] = "local-only"
		}
	}
	wg.Wait()
}

func BenchmarkModelRefreshCandidates(b *testing.B) {
	for _, antigravityCount := range []int{12, 289} {
		b.Run(fmt.Sprintf("antigravity=%d", antigravityCount), func(b *testing.B) {
			m := &Manager{auths: make(map[string]*Auth)}
			for i := range 289 {
				provider := "codex"
				if i < antigravityCount {
					provider = "antigravity"
				}
				a := &Auth{ID: fmt.Sprint(i), Provider: provider,
					Attributes:  map[string]string{"base_url": "http://catalog.invalid"},
					Metadata:    map[string]any{"access_token": "test-token", "project_id": "project"},
					ModelStates: make(map[string]*ModelState),
				}
				for j := range 48 {
					a.ModelStates[fmt.Sprint(j)] = &ModelState{Unavailable: true}
				}
				m.auths[a.ID] = a
			}
			for _, test := range []struct {
				name string
				list func() []*Auth
			}{
				{"List", m.List},
				{"Projection", func() []*Auth { return m.ListModelRefreshCandidates("antigravity") }},
			} {
				b.Run(test.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if len(test.list()) == 0 {
							b.Fatal("missing candidates")
						}
					}
				})
			}
		})
	}
}

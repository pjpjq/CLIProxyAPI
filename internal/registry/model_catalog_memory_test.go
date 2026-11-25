package registry

import (
	"reflect"
	"testing"
)

func TestStaticLookupDoesNotCloneUnrelatedCatalogs(t *testing.T) {
	model := getModels().Claude[0]
	allocs := testing.AllocsPerRun(20, func() {
		if LookupStaticModelInfo(model.ID) == nil {
			t.Fatal("model disappeared")
		}
	})
	if allocs > 20 {
		t.Fatalf("ordinary lookup allocated %.0f objects; unrelated catalogs must not be deep-cloned", allocs)
	}
}

func TestStaticLookupPreservesDevinBuiltinOverride(t *testing.T) {
	devinCatalogStore.mu.Lock()
	original := devinCatalogStore.models
	devinCatalogStore.models = []*ModelInfo{{ID: devinBuiltinSWE16SlowID, ContextLength: 1}}
	devinCatalogStore.mu.Unlock()
	t.Cleanup(func() {
		devinCatalogStore.mu.Lock()
		devinCatalogStore.models = original
		devinCatalogStore.mu.Unlock()
	})
	want := devinBuiltinSWE16SlowModelInfo()
	got := LookupStaticModelInfo(want.ID)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("builtin override = %+v, want %+v", got, want)
	}
	got.SupportedInputModalities[0] = "changed"
	if !reflect.DeepEqual(LookupStaticModelInfo(want.ID), want) {
		t.Fatal("lookup result aliases catalog")
	}
}

func BenchmarkStaticModelLookupMemory(b *testing.B) {
	for _, id := range []string{getModels().Claude[0].ID, "missing-memory-test-model", devinBuiltinSWE16SlowID} {
		b.Run(id, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				LookupStaticModelInfo(id)
			}
		})
	}
}

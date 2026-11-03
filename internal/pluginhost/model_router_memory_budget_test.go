//go:build !race

package pluginhost

import (
	"bytes"
	"context"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestRPCModelRouterMemoryBudget(t *testing.T) {
	const size = 1 << 20
	req := pluginapi.ModelRouteRequest{Body: bytes.Repeat([]byte("x"), size)}
	router := &rpcPluginAdapter{client: staticEnvelopePluginClient{raw: []byte(`{"ok":true,"result":{"handled":false}}`)}}
	host := newRouteModelHostWithRecords(capabilityRecord{id: "memory-router", plugin: pluginapi.Plugin{
		Capabilities: pluginapi.Capabilities{ModelRouter: router},
	}})
	// Keep the warmed JSON buffer in its pool; race instrumentation changes allocations.
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	if _, err := router.RouteModel(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	host.RouteModel(context.Background(), req)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("RPC routing allocated %d bytes for a %d-byte body", allocated, size)
	if allocated > 2*size {
		t.Fatalf("RPC routing allocated %d bytes for a %d-byte body; an extra body copy exceeds the budget", allocated, size)
	}
}

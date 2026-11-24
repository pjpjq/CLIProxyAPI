package pluginhost

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func memoryTestRouter(tb testing.TB, rpc bool, route modelRouterFunc) pluginapi.ModelRouter {
	tb.Helper()
	if !rpc {
		return route
	}
	lookup := newTestSymbolLookup(&testPlugin{registerResult: pluginapi.Plugin{
		Metadata: pluginapi.Metadata{
			Name: "memory-router", Version: "1.0.0", Author: "test",
			GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
		},
		Capabilities: pluginapi.Capabilities{ModelRouter: route},
	}})
	plugin, err := registerRPCPlugin(context.Background(), nil, "memory-router", lookup, pluginabi.MethodPluginRegister, nil)
	if err != nil {
		tb.Fatal(err)
	}
	return plugin.Capabilities.ModelRouter
}

func TestModelRouterMemoryRequestIsolation(t *testing.T) {
	for _, rpc := range []bool{false, true} {
		t.Run(fmt.Sprintf("RPC=%t", rpc), func(t *testing.T) {
			req := pluginapi.ModelRouteRequest{
				Body:     []byte(`{"model":"ordinary","input":"original"}`),
				Headers:  map[string][]string{"X-Test": {"original"}},
				Query:    map[string][]string{"test": {"original"}},
				Metadata: map[string]any{"nested": map[string]any{"value": "original"}},
			}
			original := bytes.Clone(req.Body)
			router := memoryTestRouter(t, rpc, func(_ context.Context, got pluginapi.ModelRouteRequest) (pluginapi.ModelRouteResponse, error) {
				if !bytes.Equal(got.Body, original) || got.Headers["X-Test"][0] != "original" ||
					got.Query["test"][0] != "original" || got.Metadata["nested"].(map[string]any)["value"] != "original" {
					t.Error("request changed across router calls")
					return pluginapi.ModelRouteResponse{}, fmt.Errorf("request changed across router calls")
				}
				got.Body[0] = '!'
				got.Headers["X-Test"][0] = "changed"
				got.Query["test"][0] = "changed"
				got.Metadata["nested"].(map[string]any)["value"] = "changed"
				return pluginapi.ModelRouteResponse{}, nil
			})
			host := newRouteModelHostWithRecords(capabilityRecord{id: "memory-router", plugin: pluginapi.Plugin{
				Capabilities: pluginapi.Capabilities{ModelRouter: router},
			}})
			// Concurrent callers may share immutable input, including maps and body.
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() { host.RouteModel(context.Background(), req) })
			}
			wg.Wait()
			if !bytes.Equal(req.Body, original) || req.Headers["X-Test"][0] != "original" ||
				req.Query["test"][0] != "original" || req.Metadata["nested"].(map[string]any)["value"] != "original" {
				t.Fatal("router mutated caller-owned input")
			}
		})
	}
}

func BenchmarkModelRouterLargeBody(b *testing.B) {
	for _, size := range []int{1 << 20, 10 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			prefix, suffix := []byte(`{"model":"ordinary","input":"`), []byte(`"}`)
			body := append(prefix, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...)
			body = append(body, suffix...)
			router := memoryTestRouter(b, true, func(_ context.Context, req pluginapi.ModelRouteRequest) (pluginapi.ModelRouteResponse, error) {
				if !bytes.Equal(req.Body, body) {
					b.Error("body changed in RPC")
					return pluginapi.ModelRouteResponse{}, fmt.Errorf("body changed in RPC")
				}
				return pluginapi.ModelRouteResponse{}, nil
			})
			host := newRouteModelHostWithRecords(capabilityRecord{id: "memory-router", plugin: pluginapi.Plugin{
				Capabilities: pluginapi.Capabilities{ModelRouter: router},
			}})
			host.RouteModel(context.Background(), pluginapi.ModelRouteRequest{Body: bytes.Clone(body)})
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for range b.N {
				// Match the handler's defensive copy before entering the host.
				host.RouteModel(context.Background(), pluginapi.ModelRouteRequest{Body: bytes.Clone(body)})
			}
		})
	}
}

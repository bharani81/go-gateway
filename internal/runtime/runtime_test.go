package runtime_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"github.com/bharanidharansrinivasan/api-gateway/internal/router"
	"github.com/bharanidharansrinivasan/api-gateway/internal/runtime"
	"go.uber.org/zap"
)

// mockRoutable builds a dummy runtime
func mockGatewayRuntime(version uint64) *runtime.GatewayRuntime {
	return &runtime.GatewayRuntime{Router: &router.Router{}, Version: version}
}

func TestRuntimeHolderAtomicSwap(t *testing.T) {
	rt1 := &runtime.GatewayRuntime{Router: &router.Router{}, Version: 1}
	holder := runtime.NewRuntimeHolder(rt1)

	if holder.Get().Version != 1 {
		t.Fatalf("expected version 1, got %d", holder.Get().Version)
	}

	rt2 := &runtime.GatewayRuntime{Router: &router.Router{}, Version: 2}
	old := holder.Swap(rt2)

	if holder.Get().Version != 2 {
		t.Fatalf("expected active version 2, got %d", holder.Get().Version)
	}
	if old.Version != 1 {
		t.Fatalf("Swap did not return the previous version (expected 1, got %d)", old.Version)
	}
}

func TestConcurrentGetAndSwap(t *testing.T) {
	holder := runtime.NewRuntimeHolder(mockGatewayRuntime(1))

	var wg sync.WaitGroup
	// 50 readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = holder.Get()
			}
		}()
	}

	// 10 writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				holder.Swap(mockGatewayRuntime(uint64(v)))
				time.Sleep(1 * time.Microsecond)
			}
		}(int64(i + 2))
	}

	wg.Wait()
	// Passed if no race condition triggered
}

// dummyPlugin implements plugin.Plugin and io.Closer
type dummyPlugin struct {
	closed bool
}

func (d *dummyPlugin) Name() string { return "dummy" }
func (d *dummyPlugin) ExecuteRequest(w http.ResponseWriter, r *http.Request) error { return nil }
func (d *dummyPlugin) ExecuteResponse(w http.ResponseWriter, r *http.Request) error { return nil }
func (d *dummyPlugin) Close() error {
	d.closed = true
	return nil
}

func TestCloseCallsChainClose(t *testing.T) {
	p := &dummyPlugin{}
	chain := plugin.NewChain([]plugin.Plugin{p}, zap.NewNop())
	chains := map[string]*plugin.Chain{
		"route-1": chain,
	}

	rt := &runtime.GatewayRuntime{Router: &router.Router{}, PluginChains: chains, Version: 1}
	rt.Close()

	if !p.closed {
		t.Fatal("expected Close() to be called on plugin inside chain when runtime is retired")
	}
}

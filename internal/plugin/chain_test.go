package plugin_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bharanidharansrinivasan/api-gateway/internal/plugin"
	"go.uber.org/zap"
)

// mockPlugin is a basic mock plugin that tracks invocations and returns predefined errors.
type mockPlugin struct {
	name             string
	execReqErr       error
	execRespErr      error
	reqCalled        bool
	respCalled       bool
	closeCalled      bool
	panicOnReq       bool
	panicOnResp      bool
}

func (m *mockPlugin) Name() string { return m.name }
func (m *mockPlugin) ExecuteRequest(w http.ResponseWriter, r *http.Request) error {
	m.reqCalled = true
	if m.panicOnReq {
		panic("mock panic during request")
	}
	return m.execReqErr
}
func (m *mockPlugin) ExecuteResponse(w http.ResponseWriter, r *http.Request) error {
	m.respCalled = true
	if m.panicOnResp {
		panic("mock panic during response")
	}
	return m.execRespErr
}
func (m *mockPlugin) Close() error {
	m.closeCalled = true
	return nil
}

func TestHappyPathContinues(t *testing.T) {
	p1 := &mockPlugin{name: "p1"}
	p2 := &mockPlugin{name: "p2"}
	c := plugin.NewChain([]plugin.Plugin{p1, p2}, zap.NewNop())

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	aborted := c.ExecuteRequest(w, r)
	if aborted {
		t.Errorf("expected aborted=false")
	}
	if !p1.reqCalled || !p2.reqCalled {
		t.Errorf("expected both plugins to be called")
	}
}

func TestFirstPluginAborts(t *testing.T) {
	p1 := &mockPlugin{name: "p1", execReqErr: &plugin.AbortError{StatusCode: 401, Message: "unauthorized"}}
	p2 := &mockPlugin{name: "p2"}
	c := plugin.NewChain([]plugin.Plugin{p1, p2}, zap.NewNop())

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	aborted := c.ExecuteRequest(w, r)
	if !aborted {
		t.Errorf("expected aborted=true")
	}
	if !p1.reqCalled {
		t.Errorf("expected p1 to be called")
	}
	if p2.reqCalled {
		t.Errorf("expected p2 NOT to be called")
	}

	if w.Code != 401 {
		t.Errorf("expected HTTP 401, got %d", w.Code)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	p1 := &mockPlugin{name: "p1", panicOnReq: true}
	c := plugin.NewChain([]plugin.Plugin{p1}, zap.NewNop())

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("chain failed to recover panic: %v", rec)
		}
	}()

	aborted := c.ExecuteRequest(w, r)
	if !aborted {
		t.Errorf("expected panicking plugin to abort chain")
	}
	if w.Code != 500 {
		t.Errorf("expected HTTP 500, got %d", w.Code)
	}
}

func TestUnexpectedErrorWrites500(t *testing.T) {
	p1 := &mockPlugin{name: "p1", execReqErr: errors.New("boom")}
	c := plugin.NewChain([]plugin.Plugin{p1}, zap.NewNop())

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	aborted := c.ExecuteRequest(w, r)
	if !aborted {
		t.Errorf("expected unexpected error to abort chain")
	}
	if w.Code != 500 {
		t.Errorf("expected HTTP 500 on unexpected error, got %d", w.Code)
	}
}

func TestResponseChainContinuesOnError(t *testing.T) {
	p1 := &mockPlugin{name: "p1", execRespErr: errors.New("log write failed")}
	p2 := &mockPlugin{name: "p2"}
	c := plugin.NewChain([]plugin.Plugin{p1, p2}, zap.NewNop())

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	c.ExecuteResponse(w, r)
	if !p1.respCalled || !p2.respCalled {
		t.Errorf("expected all plugins to be called in response chain despite errors")
	}
}

func TestCloseCallsCloserPlugins(t *testing.T) {
	p1 := &mockPlugin{name: "p1"}
	// Ensure mockPlugin implements io.Closer for this test at runtime
	c := plugin.NewChain([]plugin.Plugin{p1}, zap.NewNop())
	c.Close()

	if !p1.closeCalled {
		t.Errorf("expected Close() to be called on plugin")
	}
}

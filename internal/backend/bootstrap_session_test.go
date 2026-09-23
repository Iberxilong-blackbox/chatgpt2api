package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"chatgpt2api/internal/service"
)

type testProxyConfig string

func (p testProxyConfig) Proxy() string { return string(p) }

func TestBootstrapReusesRecentAccountSession(t *testing.T) {
	var hits atomic.Int32
	status := atomic.Int32{}
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`<html><script src="https://cdn.oaistatic.com/_next/static/chunks/c/abc123/_/main.js"></script></html>`))
	}))
	defer server.Close()

	proxy := service.NewProxyService(testProxyConfig(""))
	newClient := func() *Client {
		client := newTestBackendClient(server)
		client.proxy = proxy
		client.sessionKey = service.BrowserSessionKey(client.AccessToken)
		return client
	}

	first := newClient()
	if err := first.Bootstrap(context.Background()); err != nil {
		t.Fatalf("first Bootstrap() error = %v", err)
	}
	second := newClient()
	if err := second.Bootstrap(context.Background()); err != nil {
		t.Fatalf("second Bootstrap() error = %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("GET / hits = %d, want 1 (second client should reuse the session)", got)
	}
	if second.ClientVersion != first.ClientVersion || len(second.powSources) == 0 {
		t.Fatalf("reused bootstrap must restore page data: version %q vs %q, sources %v", second.ClientVersion, first.ClientVersion, second.powSources)
	}

	third := newClient()
	third.DiagnoseSession(context.Background())
	if got := hits.Load(); got != 2 {
		t.Fatalf("GET / hits after DiagnoseSession = %d, want 2 (diagnostics always load the page)", got)
	}

	status.Store(http.StatusForbidden)
	if err := newClient().bootstrap(context.Background(), false); err == nil {
		t.Fatal("bootstrap should fail on 403")
	}
	if _, ok := proxy.RecentBrowserBootstrap(service.BrowserSessionKey("token-1")); ok {
		t.Fatal("a failed bootstrap must reset the account session")
	}
	if err := newClient().Bootstrap(context.Background()); err == nil {
		t.Fatal("after reset the next Bootstrap must hit the network again")
	}
	if got := hits.Load(); got != 4 {
		t.Fatalf("GET / hits = %d, want 4", got)
	}
}

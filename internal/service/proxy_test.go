package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSOCKS5AddressModes(t *testing.T) {
	t.Run("socks5h keeps hostname for proxy-side DNS", func(t *testing.T) {
		got, err := socks5Address(context.Background(), "socks5h", "chatgpt.com:443")
		if err != nil {
			t.Fatalf("socks5Address() error = %v", err)
		}
		wantPrefix := []byte{0x03, byte(len("chatgpt.com"))}
		if string(got[:len(wantPrefix)]) != string(wantPrefix) {
			t.Fatalf("address prefix = %#v, want %#v", got[:len(wantPrefix)], wantPrefix)
		}
		if host := string(got[2 : 2+len("chatgpt.com")]); host != "chatgpt.com" {
			t.Fatalf("host = %q", host)
		}
		if got[len(got)-2] != 0x01 || got[len(got)-1] != 0xbb {
			t.Fatalf("port bytes = %#v", got[len(got)-2:])
		}
	})

	t.Run("socks5 sends numeric ip when target is ip literal", func(t *testing.T) {
		got, err := socks5Address(context.Background(), "socks5", net.JoinHostPort("127.0.0.1", "8080"))
		if err != nil {
			t.Fatalf("socks5Address() error = %v", err)
		}
		want := []byte{0x01, 127, 0, 0, 1, 0x1f, 0x90}
		if string(got) != string(want) {
			t.Fatalf("address = %#v, want %#v", got, want)
		}
	})
}

func TestBrowserHTTPClientKeepsSessionAndTimeout(t *testing.T) {
	client := browserHTTPClient("", 2*time.Second)
	if client == nil {
		t.Fatal("browserHTTPClient() returned nil")
	}
	if client.Jar == nil {
		t.Fatal("browserHTTPClient() should enable a cookie jar for browser-like sessions")
	}
	if client.Timeout != 2*time.Second {
		t.Fatalf("Timeout = %s, want %s", client.Timeout, 2*time.Second)
	}
}

func TestBrowserHTTPClientPreservesCallerAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Origin"); got != "https://chatgpt.com" {
			t.Fatalf("Origin = %q", got)
		}
		if got := r.Header.Get("Referer"); got != "https://chatgpt.com/" {
			t.Fatalf("Referer = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Fatal("User-Agent should be populated by browser impersonation")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := browserHTTPClient("", 2*time.Second)
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token-1")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

type staticProxyConfig string

func (p staticProxyConfig) Proxy() string { return string(p) }

func TestBrowserHTTPClientDropsChromiumClientHints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key := range r.Header {
			if isClientHintHeader(key) {
				t.Errorf("client hint header %q should not be sent with Firefox impersonation", key)
			}
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := browserHTTPClient("", 2*time.Second)
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token-1")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="145"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if req.Header.Get("Sec-Ch-Ua") == "" {
		t.Fatal("caller request headers must not be mutated")
	}
}

func TestBrowserSessionSharesCookiesPerAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "__cf_bm", Value: "abc", Path: "/"})
			return
		}
		if cookie, err := r.Cookie("__cf_bm"); err == nil {
			_, _ = w.Write([]byte(cookie.Value))
		}
	}))
	defer server.Close()

	svc := NewProxyService(staticProxyConfig(""))
	get := func(key, path string) string {
		client := svc.BrowserHTTPClientWithProfile("", 2*time.Second)
		svc.AttachBrowserSession(client, key)
		resp, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body := make([]byte, 16)
		n, _ := resp.Body.Read(body)
		return string(body[:n])
	}

	get("account-a", "/set")
	if got := get("account-a", "/read"); got != "abc" {
		t.Fatalf("same account new client cookie = %q, want abc", got)
	}
	if got := get("account-b", "/read"); got != "" {
		t.Fatalf("other account cookie = %q, want none", got)
	}
	svc.ResetBrowserSession("account-a")
	if got := get("account-a", "/read"); got != "" {
		t.Fatalf("cookie after reset = %q, want none", got)
	}
}

func TestBrowserSessionBootstrapReuse(t *testing.T) {
	svc := NewProxyService(staticProxyConfig(""))
	if _, ok := svc.RecentBrowserBootstrap("account-a"); ok {
		t.Fatal("no bootstrap recorded yet")
	}
	svc.MarkBrowserBootstrapped("account-a", "<html>page</html>")
	page, ok := svc.RecentBrowserBootstrap("account-a")
	if !ok || page != "<html>page</html>" {
		t.Fatalf("RecentBrowserBootstrap = %q, %v", page, ok)
	}
	if _, ok := svc.RecentBrowserBootstrap("account-b"); ok {
		t.Fatal("bootstrap must be tracked per account")
	}
	if _, ok := svc.RecentBrowserBootstrap(""); ok {
		t.Fatal("anonymous sessions must never reuse a bootstrap")
	}
	svc.sessions.sessions["account-a"].bootstrappedAt = time.Now().Add(-BrowserSessionBootstrapTTL - time.Second)
	if _, ok := svc.RecentBrowserBootstrap("account-a"); ok {
		t.Fatal("expired bootstrap must not be reused")
	}
	var nilSvc *ProxyService
	nilSvc.MarkBrowserBootstrapped("account-a", "x")
	nilSvc.ResetBrowserSession("account-a")
	nilSvc.AttachBrowserSession(&http.Client{}, "account-a")
}

package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresForwardedHeadersFromUntrustedRemote(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/settings", nil)
	req.RemoteAddr = "203.0.113.10:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.99")
	req.Header.Set("X-Real-IP", "198.51.100.100")

	if got := clientIP(req); got != "203.0.113.10" {
		t.Fatalf("clientIP() = %q, want remote address", got)
	}
}

func TestClientIPTrustsForwardedHeadersFromTrustedProxy(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/settings", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.99, 127.0.0.1")

	if got := clientIP(req); got != "198.51.100.99" {
		t.Fatalf("clientIP() = %q, want forwarded client", got)
	}
}

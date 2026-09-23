package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchRemoteInfoSharesBootstrapWithinTTL(t *testing.T) {
	var bootstraps atomic.Int32
	var cookieSeen atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			bootstraps.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "__cf_bm", Value: "cf-1", Path: "/"})
			_, _ = w.Write([]byte("<html></html>"))
		case "/backend-api/me":
			if cookie, err := r.Cookie("__cf_bm"); err == nil && cookie.Value == "cf-1" {
				cookieSeen.Store(true)
			}
			writeJSON(t, w, map[string]any{"email": "user@example.com", "id": "user-1"})
		case "/backend-api/conversation/init":
			writeJSON(t, w, map[string]any{"limits_progress": []map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	accounts := newTestAccountService(t)
	accounts.remoteBaseURL = server.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return &http.Client{Transport: server.Client().Transport}
	}
	accounts.AddAccounts([]string{"token-1"})

	for i := 0; i < 2; i++ {
		if _, err := accounts.FetchRemoteInfo(context.Background(), "token-1"); err != nil {
			t.Fatalf("FetchRemoteInfo() #%d error = %v", i+1, err)
		}
	}
	if got := bootstraps.Load(); got != 1 {
		t.Fatalf("bootstraps = %d, want 1 within the session TTL", got)
	}
	if !cookieSeen.Load() {
		t.Fatal("bootstrap cookies should be sent on later requests of the same account session")
	}
}

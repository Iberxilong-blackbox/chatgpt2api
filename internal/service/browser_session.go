package service

import (
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// BrowserSessionBootstrapTTL is how long one successful GET https://chatgpt.com/
// is reused for the same account. A real browser tab loads the page once and then
// keeps calling the API with the Cloudflare cookies it received; re-fetching the
// page with a fresh cookie jar on every request doubles traffic on the exit IP and
// looks like a first-time visitor each time. __cf_bm lives about 30 minutes.
const BrowserSessionBootstrapTTL = 10 * time.Minute

type browserSession struct {
	jar            http.CookieJar
	bootstrappedAt time.Time
}

// browserSessionStore keeps one cookie jar per account so the account refresh
// path and the upstream request path share Cloudflare/ChatGPT cookies, plus the
// most recent chatgpt.com page (it is the same for every account) so a skipped
// bootstrap can still resolve PoW scripts and the client build.
type browserSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*browserSession
	page     string
}

func newBrowserSessionStore() *browserSessionStore {
	return &browserSessionStore{sessions: map[string]*browserSession{}}
}

func (s *browserSessionStore) session(key string) *browserSession {
	session := s.sessions[key]
	if session == nil {
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		session = &browserSession{jar: jar}
		s.sessions[key] = session
	}
	return session
}

// BrowserSessionKey returns the shared-session key for an access token, or ""
// when the request is anonymous and must not share cookies.
func BrowserSessionKey(accessToken string) string {
	if accessToken == "" {
		return ""
	}
	return AccountIDFromToken(accessToken)
}

// AttachBrowserSession makes client use the shared cookie jar of key.
func (s *ProxyService) AttachBrowserSession(client *http.Client, key string) {
	if s == nil || s.sessions == nil || client == nil || key == "" {
		return
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	client.Jar = s.sessions.session(key).jar
}

// RecentBrowserBootstrap returns the cached chatgpt.com page when key completed
// a bootstrap within BrowserSessionBootstrapTTL.
func (s *ProxyService) RecentBrowserBootstrap(key string) (string, bool) {
	if s == nil || s.sessions == nil || key == "" {
		return "", false
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	session := s.sessions.sessions[key]
	if session == nil || session.bootstrappedAt.IsZero() || s.sessions.page == "" {
		return "", false
	}
	if time.Since(session.bootstrappedAt) > BrowserSessionBootstrapTTL {
		return "", false
	}
	return s.sessions.page, true
}

// MarkBrowserBootstrapped records a successful bootstrap for key.
func (s *ProxyService) MarkBrowserBootstrapped(key, page string) {
	if s == nil || s.sessions == nil || key == "" {
		return
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	s.sessions.session(key).bootstrappedAt = time.Now()
	if page != "" {
		s.sessions.page = page
	}
}

// ResetBrowserSession drops the cookies and bootstrap state of key, e.g. after
// Cloudflare challenged it, so the next attempt starts a clean session.
func (s *ProxyService) ResetBrowserSession(key string) {
	if s == nil || s.sessions == nil || key == "" {
		return
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	delete(s.sessions.sessions, key)
}

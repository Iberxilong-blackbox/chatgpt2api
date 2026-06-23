package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type loginRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	attempts map[string]loginAttempts
}

type loginAttempts struct {
	count     int
	expiresAt time.Time
}

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &loginRateLimiter{
		limit:    limit,
		window:   window,
		attempts: map[string]loginAttempts{},
	}
}

func (l *loginRateLimiter) Allow(key string, now time.Time) bool {
	if l == nil || key == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	item, ok := l.attempts[key]
	if !ok || !now.Before(item.expiresAt) {
		return true
	}
	return item.count < l.limit
}

func (l *loginRateLimiter) RecordFailure(key string, now time.Time) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	item, ok := l.attempts[key]
	if !ok || !now.Before(item.expiresAt) {
		item = loginAttempts{expiresAt: now.Add(l.window)}
	}
	item.count++
	l.attempts[key] = item
}

func (l *loginRateLimiter) Reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func loginRateLimitKey(r *http.Request, username string) string {
	ip := clientIP(r)
	if ip == "" {
		ip = remoteAddrHost(r)
	}
	return ip
}

func remoteAddrHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func registerRateLimitKey(r *http.Request, identityID, username string) string {
	ip := clientIP(r)
	if ip == "" {
		ip = remoteAddrHost(r)
	}
	identityID = strings.TrimSpace(identityID)
	username = strings.TrimSpace(username)
	return ip + ":" + identityID + ":" + username
}

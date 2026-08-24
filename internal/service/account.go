package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

// Account map fields stored by the storage layer.
//
//	access_token        - ChatGPT access token (JWT), required, unique account identifier
//	session_token       - Session token used to refresh access_token automatically
//	type                - Account type: Free / Plus / ProLite / Pro / Team
//	status              - Account status: normal / invalid / limited / disabled / refresh pending / refreshing
//	quota               - Remaining quota count
//	image_quota_unknown - Whether the image quota is unknown
//	email               - Linked email address
//	user_id             - User ID
//	chatgpt_account_id  - ChatGPT account ID
//	limits_progress     - Usage limit progress
//	default_model_slug  - Default model slug
//	restore_at          - Quota restore time
type AccountConfig interface {
	AutoRemoveInvalidAccounts() bool
	AutoRemoveRateLimitedAccounts() bool
}

type DailyAccountRefreshConfig interface {
	DailyAccountRefreshEnabled() bool
	DailyAccountRefreshStartTime() string
	DailyAccountRefreshEndTime() string
}

type AccountService struct {
	mu                       sync.Mutex
	storage                  storage.Backend
	config                   AccountConfig
	proxy                    *ProxyService
	logs                     *LogService
	index                    int
	items                    []map[string]any
	imageReservations        map[string]int
	remoteBaseURL            string
	browserHTTPClient        func(profile string, timeout time.Duration) *http.Client
	textRequestCount         map[string]int
	textCooldownUntil        time.Time
	refresher                *SessionRefresher
	warmingWorker            WarmingRunner
	importDir                string
	lastRefreshAttempt       map[string]time.Time
	reservoirMu              sync.Mutex
	reservoirRunning         bool
	reservoirPaused          bool
	reservoirRefreshing      int
	reservoirLastRunAt       *time.Time
	reservoirLastRefillAt    *time.Time
	reservoirLastMaintenance *time.Time
	reservoirLastResult      map[string]any
}

const (
	defaultRemoteUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	defaultRemoteSecCHUA   = `"Not:A-Brand";v="99", "Google Chrome";v="145", "Chromium";v="145"`
	defaultRemoteProfile   = "chrome133"
)

func NewAccountService(backend storage.Backend, config AccountConfig, proxy *ProxyService, logs *LogService) *AccountService {
	browserHTTPClient := func(profile string, timeout time.Duration) *http.Client {
		if proxy == nil {
			return &http.Client{Timeout: timeout}
		}
		return proxy.BrowserHTTPClientWithProfile(profile, timeout)
	}
	s := &AccountService{
		storage:            backend,
		config:             config,
		proxy:              proxy,
		logs:               logs,
		imageReservations:  map[string]int{},
		remoteBaseURL:      "https://chatgpt.com",
		browserHTTPClient:  browserHTTPClient,
		textRequestCount:   map[string]int{},
		lastRefreshAttempt: map[string]time.Time{},
	}
	// Initialize SessionRefresher with the uTLS client for /api/auth/session.
	s.refresher = NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		client := s.browserHTTPClient(defaultRemoteProfile, refreshTimeout)
		if client == nil {
			client = &http.Client{Timeout: refreshTimeout}
		}
		return client.Do(req)
	})
	s.items = s.loadAccounts()
	return s
}

func (s *AccountService) ListTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.items))
	for _, item := range s.items {
		if token := util.Clean(item["access_token"]); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) ListTokensByIDs(ids []string) []string {
	targets := cleanAccountIDs(ids)
	if len(targets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(targets))
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := targets[accountIDFromToken(token)]; ok {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) GetTokenByID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token != "" && accountIDFromToken(token) == id {
			return token
		}
	}
	return ""
}

func (s *AccountService) ListAccounts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return publicAccounts(s.items)
}

func (s *AccountService) ListLimitedTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, item := range s.items {
		if item["status"] == "限流" {
			if token := util.Clean(item["access_token"]); token != "" {
				out = append(out, token)
			}
		}
	}
	return out
}

func (s *AccountService) listRefreshableLimitedTokens(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, item := range s.items {
		if item["status"] != "限流" && item["status"] != "过期待刷新" {
			continue
		}
		if isWarmingAccount(item) {
			continue
		}
		if item["status"] == "限流" {
			if restoreAt, ok := parseAccountRestoreAt(item["restore_at"]); ok && restoreAt.After(now) {
				continue
			}
		}
		if token := util.Clean(item["access_token"]); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) listRefreshableWarmingTokens(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, item := range s.items {
		if !isWarmingAccount(item) {
			continue
		}
		if util.Clean(item["status"]) != "过期待刷新" {
			if restoreAt, ok := parseAccountRestoreAt(item["restore_at"]); !ok || restoreAt.After(now) {
				continue
			}
		}
		if token := util.Clean(item["access_token"]); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) AddAccounts(tokens []string) map[string]any {
	records := make([]map[string]any, 0, len(tokens))
	for _, token := range tokens {
		records = append(records, map[string]any{"access_token": token})
	}
	return s.AddAccountRecords(records)
}

func (s *AccountService) AddAccountRecords(records []map[string]any) map[string]any {
	cleaned := cleanAccountRecords(records)
	if len(cleaned) == 0 {
		return map[string]any{"added": 0, "skipped": 0, "items": s.ListAccounts()}
	}
	s.mu.Lock()
	indexed := map[string]map[string]any{}
	order := make([]string, 0, len(s.items)+len(cleaned))
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		indexed[token] = util.CopyMap(item)
		order = append(order, token)
	}
	added, skipped, updated := 0, 0, 0
	for _, record := range cleaned {
		token := util.Clean(record["access_token"])
		current, ok := indexed[token]
		if ok {
			skipped++
		} else {
			// Dedup by email / user_id: same identity with a different
			// access_token merges into the existing account instead of
			// creating a duplicate.
			email := strings.ToLower(util.Clean(record["email"]))
			userID := util.Clean(record["user_id"])
			if email != "" || userID != "" {
				for existingToken, existing := range indexed {
					if email != "" && strings.ToLower(util.Clean(existing["email"])) == email {
						ok = true
						current = existing
						delete(indexed, existingToken)
						order = removeString(order, existingToken)
						break
					}
					if userID != "" && util.Clean(existing["user_id"]) == userID {
						ok = true
						current = existing
						delete(indexed, existingToken)
						order = removeString(order, existingToken)
						break
					}
				}
			}
			if ok {
				updated++
			} else {
				added++
				current = map[string]any{}
			}
			order = append(order, token)
		}
		updates := map[string]any{"access_token": token, "type": util.ValueOr(current["type"], "Free")}
		if !ok && current["imported_at"] == nil {
			updates["imported_at"] = util.NowISO()
		}
		if accountType := importedAccountType(record); accountType != "" {
			updates["type"] = accountType
		}
		// Generate persistent fingerprint for accounts that don't have one yet.
		// Once set, fp is NEVER overwritten — oai-device-id stays fixed for life.
		if current["fp"] == nil {
			if fp := prepareAccountFP(record); fp != nil {
				updates["fp"] = fp
			}
		}
		// Carry over account metadata from the JSON record so that accounts
		// imported from files have email, user_id, etc. set immediately
		// without requiring a remote refresh.
		for _, key := range []string{"email", "user_id", "chatgpt_account_id", "refresh_token", "id_token", "expired"} {
			if val := util.Clean(record[key]); val != "" && current[key] == nil {
				updates[key] = val
			}
		}
		// Prefer nested session JSON because top-level exports can contain stale
		// or empty compatibility fields alongside the live session payload.
		if current["session_token"] == nil {
			st := ""
			if sess, ok := record["session"].(map[string]any); ok {
				st = util.Clean(sess["sessionToken"])
			}
			if st == "" {
				if raw, ok := record["session_raw"].(map[string]any); ok {
					st = util.Clean(raw["sessionToken"])
				}
			}
			if st == "" {
				st = util.Clean(record["sessionToken"])
			}
			if st == "" {
				st = util.Clean(record["session_token"])
			}
			if st != "" {
				updates["session_token"] = st
			}
		}
		if updates["chatgpt_account_id"] == nil {
			if val := util.Clean(record["account_id"]); val != "" && current["account_id"] == nil {
				updates["chatgpt_account_id"] = val
			}
		}
		normalized := normalizeAccount(mergeMaps(current, updates))
		if normalized != nil {
			indexed[token] = normalized
		}
	}
	next := make([]map[string]any, 0, len(order))
	seen := map[string]struct{}{}
	for _, token := range order {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		next = append(next, indexed[token])
	}
	s.items = next
	_ = s.saveLocked()
	items := publicAccounts(s.items)
	s.mu.Unlock()
	s.logs.Add(fmt.Sprintf("新增 %d 个账号，更新 %d 个，跳过 %d 个", added, updated, skipped), map[string]any{
		"module":         "accounts",
		"operation_type": "新增",
		"added":          added,
		"updated":        updated,
		"skipped":        skipped,
	})
	return map[string]any{"added": added, "updated": updated, "skipped": skipped, "items": items}
	}

func removeString(slice []string, s string) []string {
	for i, v := range slice {
		if v == s {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func (s *AccountService) AddAccountFromSession(sessionJSON string) (map[string]any, error) {
	var session struct {
		AccessToken  string `json:"accessToken"`
		Expires      any    `json:"expires"`
		SessionToken string `json:"sessionToken"`
		User         struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(sessionJSON), &session); err != nil {
		return nil, fmt.Errorf("invalid session JSON: %w", err)
	}

	// Parse full JSON for fingerprint extraction (best-effort, non-fatal)
	var record map[string]any
	json.Unmarshal([]byte(sessionJSON), &record)

	accessToken := util.Clean(session.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("session JSON missing accessToken")
	}
	sessionToken := util.Clean(session.SessionToken)
	if sessionToken == "" {
		return nil, fmt.Errorf("session JSON missing sessionToken")
	}

	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	validated, err := s.refresher.RefreshSession(ctx, accessToken, sessionToken)
	if err != nil {
		return nil, fmt.Errorf("session token validation failed: %w", err)
	}
	accessToken = util.Clean(validated.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("session token validation failed: missing access token")
	}
	sessionToken = util.Clean(validated.SessionToken)
	if sessionToken == "" {
		sessionToken = util.Clean(session.SessionToken)
	}
	sessionExpires := any(firstNonEmpty(util.Clean(validated.Expires), util.Clean(session.Expires)))
	userID := util.Clean(validated.User.ID)
	email := util.Clean(validated.User.Email)
	updates := map[string]any{
		"session_token":      sessionToken,
		"session_expires":    sessionExpires,
		"token_refreshed_at": util.NowISO(),
	}
	if userID != "" {
		updates["user_id"] = userID
	}
	if email != "" {
		updates["email"] = email
	}
	if name := util.Clean(validated.User.Name); name != "" {
		updates["name"] = name
	}
	if record != nil {
		updates["fp"] = prepareAccountFP(record)
	}

	matchedToken := s.findSessionImportAccountToken(accessToken, userID, email)
	result := map[string]any{"added": 0, "skipped": 0, "updated": 0, "items": s.ListAccounts()}
	if matchedToken != "" {
		if !s.UpdateAccountFromSessionImport(matchedToken, accessToken, updates, true) {
			return nil, fmt.Errorf("session account update failed")
		}
		result["updated"] = 1
	} else {
		result = s.AddAccounts([]string{accessToken})
	}
	if item := s.UpdateAccount(accessToken, updates); item != nil {
		publicItems := publicAccounts([]map[string]any{item})
		if len(publicItems) > 0 {
			result["item"] = publicItems[0]
		}
		result["items"] = s.ListAccounts()
	}
	result["tokens"] = []string{accessToken}
	return result, nil
}

func isRecoverableSessionImportStatus(status string) bool {
	switch status {
	case "异常", "过期待刷新", "刷新中":
		return true
	default:
		return false
	}
}

func (s *AccountService) findSessionImportAccountToken(accessToken, userID, email string) string {
	accessToken = util.Clean(accessToken)
	userID = util.Clean(userID)
	email = strings.ToLower(util.Clean(email))

	s.mu.Lock()
	defer s.mu.Unlock()

	if accessToken != "" && s.findIndexLocked(accessToken) >= 0 {
		return accessToken
	}
	if userID != "" {
		for _, item := range s.items {
			if util.Clean(item["user_id"]) == userID {
				return util.Clean(item["access_token"])
			}
		}
	}
	if email != "" {
		for _, item := range s.items {
			if strings.ToLower(util.Clean(item["email"])) == email {
				return util.Clean(item["access_token"])
			}
		}
	}
	return ""
}

func (s *AccountService) DeleteAccounts(tokens []string) map[string]any {
	targets := map[string]struct{}{}
	for _, token := range cleanTokens(tokens) {
		targets[token] = struct{}{}
	}
	if len(targets) == 0 {
		return map[string]any{"removed": 0, "items": s.ListAccounts()}
	}
	s.mu.Lock()
	next := s.items[:0]
	removed := 0
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if _, ok := targets[token]; ok {
			removed++
			delete(s.imageReservations, token)
			delete(s.textRequestCount, token)
			continue
		}
		next = append(next, item)
	}
	s.items = next
	if len(s.items) > 0 {
		s.index %= len(s.items)
	} else {
		s.index = 0
	}
	if removed > 0 {
		_ = s.saveLocked()
	}
	items := publicAccounts(s.items)
	s.mu.Unlock()
	if removed > 0 {
		s.logs.Add(fmt.Sprintf("删除 %d 个账号", removed), map[string]any{
			"module":         "accounts",
			"operation_type": "删除",
			"removed":        removed,
		})
	}
	return map[string]any{"removed": removed, "items": items}
}

func (s *AccountService) RemoveToken(token string) bool {
	return util.ToInt(s.DeleteAccounts([]string{token})["removed"], 0) > 0
}

// ReconcileDeactivatedImportedAccounts disables active records whose archived
// source JSON has an explicit OpenAI account_deactivated marker.
func (s *AccountService) ReconcileDeactivatedImportedAccounts(importDir string) (int, int, error) {
	entries, err := os.ReadDir(filepath.Join(importDir, "imported"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	emails := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(importDir, "imported", entry.Name()))
		if err != nil {
			continue
		}
		var record map[string]any
		if json.Unmarshal(data, &record) != nil || !truthy(record["is-ban"]) {
			continue
		}
		if email := strings.ToLower(util.Clean(record["email"])); email != "" {
			emails[email] = struct{}{}
		}
	}
	if len(emails) == 0 {
		return 0, 0, nil
	}

	s.mu.Lock()
	deactivated := 0
	disabled := 0
	for index, item := range s.items {
		email := strings.ToLower(util.Clean(item["email"]))
		if _, ok := emails[email]; !ok {
			continue
		}
		deactivated++
		if util.Clean(item["status"]) == "禁用" && util.Clean(item["deactivation_reason"]) == "account_deactivated" {
			continue
		}
		updated := normalizeAccount(mergeMaps(item, map[string]any{
			"status":              "禁用",
			"quota":               0,
			"image_quota_unknown": false,
			"session_token":       "",
			"deactivation_reason": "account_deactivated",
			"deactivated_at":      util.NowISO(),
		}))
		if updated == nil {
			continue
		}
		s.items[index] = updated
		delete(s.imageReservations, util.Clean(item["access_token"]))
		delete(s.textRequestCount, util.Clean(item["access_token"]))
		disabled++
	}
	if disabled > 0 {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	if disabled > 0 {
		s.logs.Add("同步停用账号", map[string]any{
			"module":         "accounts",
			"operation_type": "停用",
			"disabled":       disabled,
		})
	}
	return deactivated, disabled, nil
}

func (s *AccountService) UpdateAccount(accessToken string, updates map[string]any) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	if _, ok := updates["quota"]; ok {
		if _, hasQuotaCheckedAt := updates["quota_checked_at"]; !hasQuotaCheckedAt {
			updates = mergeMaps(updates, map[string]any{"quota_checked_at": util.NowISO()})
		}
	}
	if _, ok := updates["image_quota_unknown"]; ok {
		if _, hasQuotaCheckedAt := updates["quota_checked_at"]; !hasQuotaCheckedAt {
			updates = mergeMaps(updates, map[string]any{"quota_checked_at": util.NowISO()})
		}
	}
	merged := mergeMaps(s.items[idx], updates, map[string]any{"access_token": accessToken})
	if delta := util.ToInt(merged["zero_quota_refresh_count_delta"], 0); delta != 0 {
		merged["zero_quota_refresh_count"] = util.ToInt(s.items[idx]["zero_quota_refresh_count"], 0) + delta
		delete(merged, "zero_quota_refresh_count_delta")
	}
	account := normalizeAccount(merged)
	if account == nil {
		return nil
	}
	if account["status"] == "限流" && s.config.AutoRemoveRateLimitedAccounts() {
		delete(s.imageReservations, accessToken)
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		_ = s.saveLocked()
		s.logs.Add("自动移除限流账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"token":          util.AnonymizeToken(accessToken),
		})
		return nil
	}
	s.items[idx] = account
	_ = s.saveLocked()
	s.logs.Add("更新账号", map[string]any{
		"module":         "accounts",
		"operation_type": "更新",
		"token":          util.AnonymizeToken(accessToken),
		"status":         account["status"],
	})
	return util.CopyMap(account)
}

func (s *AccountService) UpdateAccountFromSessionImport(oldAccessToken, newAccessToken string, updates map[string]any, recoverStatus bool) bool {
	oldAccessToken = util.Clean(oldAccessToken)
	newAccessToken = util.Clean(newAccessToken)
	if oldAccessToken == "" || newAccessToken == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.findIndexLocked(oldAccessToken)
	if idx < 0 {
		return false
	}
	if oldAccessToken != newAccessToken {
		if duplicateIdx := s.findIndexLocked(newAccessToken); duplicateIdx >= 0 && duplicateIdx != idx {
			s.items = append(s.items[:duplicateIdx], s.items[duplicateIdx+1:]...)
			if duplicateIdx < idx {
				idx--
			}
		}
	}

	accountUpdates := mergeMaps(updates, map[string]any{"access_token": newAccessToken})
	if recoverStatus && isRecoverableSessionImportStatus(util.Clean(s.items[idx]["status"])) {
		accountUpdates["status"] = "正常"
	}
	account := normalizeAccount(mergeMaps(s.items[idx], accountUpdates))
	if account == nil {
		return false
	}
	s.items[idx] = account
	if oldAccessToken != newAccessToken {
		if count, ok := s.imageReservations[oldAccessToken]; ok {
			s.imageReservations[newAccessToken] = count
			delete(s.imageReservations, oldAccessToken)
		}
		if count, ok := s.textRequestCount[oldAccessToken]; ok {
			s.textRequestCount[newAccessToken] = count
			delete(s.textRequestCount, oldAccessToken)
		}
	}
	_ = s.saveLocked()
	s.logs.Add("更新Session账号", map[string]any{
		"module":         "accounts",
		"operation_type": "更新",
		"token":          util.AnonymizeToken(newAccessToken),
		"status":         account["status"],
	})
	return true
}

func (s *AccountService) GetAccount(accessToken string) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	return util.CopyMap(s.items[idx])
}

const MaxTokenSwitchAttempts = 5

func (s *AccountService) GetTextAccessToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	nonFree := s.filterNonFreeLocked()
	if len(nonFree) > 0 {
		return s.selectFromTextPoolLocked(nonFree, false)
	}

	free := s.filterFreeLocked()
	if len(free) > 0 {
		return s.selectFromTextPoolLocked(free, true)
	}

	return ""
}

func (s *AccountService) GetTextAccessTokenWithRetry(exhaustedTokens map[string]struct{}) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	nonFree := s.filterNonFreeLocked()
	free := s.filterFreeLocked()

	if token := s.selectTextTokenFromPoolLocked(nonFree, false, exhaustedTokens); token != "" {
		return token, true
	}
	if token := s.selectTextTokenFromPoolLocked(free, true, exhaustedTokens); token != "" {
		return token, true
	}
	return "", false
}

// HasOtherAvailableToken checks whether there is at least one usable text token
// besides the given accessToken. It excludes exhausted tokens, warming accounts,
// and accounts with status 禁用/异常/刷新中/过期待刷新.
// This is a read-only check with no side effects.
func (s *AccountService) HasOtherAvailableToken(accessToken string, exhaustedTokens map[string]struct{}) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	pools := [][]map[string]any{s.filterNonFreeLocked(), s.filterFreeLocked()}
	for _, pool := range pools {
		for _, item := range pool {
			token := util.Clean(item["access_token"])
			if token == "" || token == accessToken {
				continue
			}
			if _, exhausted := exhaustedTokens[token]; exhausted {
				continue
			}
			return true
		}
	}
	return false
}

func (s *AccountService) HandleTokenExpiredOnRequest(expiredToken string) (newToken string, shouldRetry bool) {
	account := s.GetAccount(expiredToken)
	if account == nil {
		return "", false
	}

	sessionToken := util.Clean(account["session_token"])
	if sessionToken == "" {
		return "", false
	}
	if s.UpdateAccount(expiredToken, map[string]any{"status": "刷新中"}) == nil {
		return "", false
	}
	s.refreshAccountViaSessionAsync(expiredToken, sessionToken)
	return "", true
}

func (s *AccountService) refreshAccountViaSessionAsync(accessToken, sessionToken string) {
	if s.refresher.IsRefreshing(accessToken) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()

		newAccessToken, newSessionToken, newExpires, err := s.refresher.RefreshToken(ctx, accessToken, sessionToken)
		if err != nil {
			s.recordAccountRefreshFailure(accessToken, "session_refresh", err)
			return
		}
		if !s.RefreshAccountViaSession(accessToken, newAccessToken, newSessionToken, newExpires) {
			s.recordAccountRefreshFailure(accessToken, "session_refresh", fmt.Errorf("账号更新失败"))
			return
		}
		_ = s.refreshAccountInfoAfterSession(ctx, newAccessToken)
	}()
}

const refreshCooldown = 5 * time.Minute

// canRefresh checks whether the given access token is allowed to be refreshed.
// Returns false if the token was refreshed within the last refreshCooldown (5 minutes).
func (s *AccountService) canRefresh(accessToken string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.lastRefreshAttempt[accessToken]
	if !ok {
		return true
	}
	return time.Since(last) >= refreshCooldown
}

// markRefreshed records a refresh attempt timestamp for the given access token.
func (s *AccountService) markRefreshed(accessToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRefreshAttempt[accessToken] = time.Now()
}

// TrySyncRefresh synchronously refreshes the account's access_token using its session_token.
// It is intended as a last resort when no other accounts are available.
// Returns (newAccessToken, true) on success, or ("", false) if the account is not found,
// has no session_token, is in the cooldown period, or the refresh HTTP request fails.
// On success the account data is updated and the cooldown timer is reset.
func (s *AccountService) TrySyncRefresh(accessToken string) (string, bool) {
	account := s.GetAccount(accessToken)
	if account == nil {
		return "", false
	}
	sessionToken := util.Clean(account["session_token"])
	if sessionToken == "" {
		return "", false
	}
	if !s.canRefresh(accessToken) {
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	newAT, newST, newExp, err := s.refresher.RefreshToken(ctx, accessToken, sessionToken)
	if err != nil {
		return "", false
	}
	if !s.RefreshAccountViaSession(accessToken, newAT, newST, newExp) {
		return "", false
	}
	_ = s.refreshAccountInfoAfterSession(ctx, newAT)
	s.markRefreshed(accessToken)
	return newAT, true
}

func (s *AccountService) filterNonFreeLocked() []map[string]any {
	var out []map[string]any
	for _, item := range s.items {
		status := util.Clean(item["status"])
		if status == "禁用" || status == "异常" || status == "刷新中" || status == "过期待刷新" {
			continue
		}
		if isWarmingAccount(item) {
			continue
		}
		if !isReservoirTextCandidate(item) {
			continue
		}
		if IsPaidImageAccount(item) {
			out = append(out, item)
		}
	}
	return out
}

func (s *AccountService) filterFreeLocked() []map[string]any {
	var out []map[string]any
	for _, item := range s.items {
		status := util.Clean(item["status"])
		if status == "禁用" || status == "异常" || status == "刷新中" || status == "过期待刷新" {
			continue
		}
		if isWarmingAccount(item) {
			continue
		}
		if !isReservoirTextCandidate(item) {
			continue
		}
		if !IsPaidImageAccount(item) {
			out = append(out, item)
		}
	}
	return out
}

func (s *AccountService) selectFromTextPoolLocked(pool []map[string]any, isFree bool) string {
	return s.selectTextTokenFromPoolLocked(pool, isFree, nil)
}

func (s *AccountService) selectTextTokenFromPoolLocked(pool []map[string]any, isFree bool, exhaustedTokens map[string]struct{}) string {
	const maxRequestsPerAccount = 10

	if len(pool) == 0 {
		return ""
	}
	if isFree {
		now := time.Now()
		if s.textCooldownUntil.IsZero() || now.After(s.textCooldownUntil) {
			s.resetTextCountsLocked(pool)
			s.textCooldownUntil = now.Add(5 * time.Hour)
		}
	}

	var bestToken string
	bestCount := int(^uint(0) >> 1)
	allExhausted := true
	for _, item := range pool {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, exhausted := exhaustedTokens[token]; exhausted {
			continue
		}
		count := s.textRequestCount[token]
		if isFree && count >= maxRequestsPerAccount {
			continue
		}
		if count < bestCount {
			bestCount = count
			bestToken = token
		}
		if count < maxRequestsPerAccount {
			allExhausted = false
		}
	}

	if bestToken == "" {
		return ""
	}
	if allExhausted {
		if !isFree && len(pool) > 1 {
			s.resetTextCountsLocked(pool)
			bestCount = 0
		}
	}

	s.textRequestCount[bestToken] = bestCount + 1
	return bestToken
}

func (s *AccountService) resetTextCountsLocked(pool []map[string]any) {
	for _, item := range pool {
		s.textRequestCount[util.Clean(item["access_token"])] = 0
	}
}

func (s *AccountService) GetAvailableAccessToken(ctx context.Context) (string, error) {
	return s.GetAvailableAccessTokenFor(ctx, nil)
}

func (s *AccountService) GetAvailableAccessTokenFor(ctx context.Context, allow func(map[string]any) bool) (string, error) {
	return s.GetAvailableAccessTokenForWithObserver(ctx, allow, nil)
}

func (s *AccountService) GetAvailableAccessTokenForWithObserver(ctx context.Context, allow func(map[string]any) bool, observe func(ImageAccountSelectionEvent)) (string, error) {
	attempted := map[string]struct{}{}
	var lastRefreshErr error
	for {
		reservation, err := s.reserveNextCandidateToken(attempted, allow)
		if err != nil {
			if lastRefreshErr != nil {
				return "", lastRefreshErr
			}
			return "", err
		}
		attempted[reservation.token] = struct{}{}
		before := s.GetAccount(reservation.token)
		account, refreshErr := s.RefreshAccountState(ctx, reservation.token)
		if refreshErr != nil {
			lastRefreshErr = refreshErr
			if observe != nil {
				observe(imageAccountSelectionEvent("refresh_failed", reservation.token, before, s.GetAccount(reservation.token), refreshErr))
			}
			if cached := s.cachedAccountForTransientRefreshError(reservation.token, refreshErr); cached != nil &&
				(allow == nil || allow(cached)) &&
				s.reservedImageSlotAvailable(reservation) {
				if observe != nil {
					observe(imageAccountSelectionEvent("selected_cached", reservation.token, before, cached, nil))
				}
				return reservation.token, nil
			}
		}
		if account != nil && (allow == nil || allow(account)) && s.reservedImageSlotAvailable(reservation) {
			if observe != nil {
				observe(imageAccountSelectionEvent("selected", reservation.token, before, account, nil))
			}
			return reservation.token, nil
		}
		if observe != nil {
			observe(imageAccountSelectionEvent("rejected_after_refresh", reservation.token, before, s.GetAccount(reservation.token), refreshErr))
		}
		s.releaseImageReservation(reservation.token)
	}
}

func (s *AccountService) cachedAccountForTransientRefreshError(accessToken string, err error) map[string]any {
	if err == nil {
		return nil
	}
	if _, ok := util.SummarizeUpstreamConnectionError(err.Error()); !ok {
		return nil
	}
	account := s.GetAccount(accessToken)
	if account == nil {
		return nil
	}
	if IsImageAccountAvailable(account) {
		return account
	}
	return nil
}

func (s *AccountService) HasAvailableAccount() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if s.availableImageSlotsLocked(item) > 0 {
			return true
		}
	}
	return false
}

func (s *AccountService) RefreshAccountState(ctx context.Context, accessToken string) (map[string]any, error) {
	remote, err := s.FetchRemoteInfo(ctx, accessToken)
	if err != nil {
		if _, handled := s.ApplyAccountError(accessToken, "refresh_account_state", err); handled {
			return s.GetAccount(accessToken), nil
		}
		return nil, err
	}
	return s.UpdateAccount(accessToken, accountRefreshSuccessUpdates(remote)), nil
}

type pendingRefreshItem struct {
	accessToken  string
	sessionToken string
}

func accountRefreshSuccessUpdates(remote map[string]any) map[string]any {
	updates := util.CopyMap(remote)
	now := util.NowISO()
	updates["quota_checked_at"] = now
	updates["last_refresh_error"] = nil
	updates["last_refresh_error_stage"] = nil
	updates["last_refresh_error_at"] = nil
	quota := util.ToInt(updates["quota"], 0)
	unknown := util.ToBool(updates["image_quota_unknown"])
	if quota > 0 {
		updates["status"] = "正常"
		updates["last_nonzero_quota"] = quota
		updates["zero_quota_refresh_count"] = 0
	} else if !unknown {
		updates["status"] = "限流"
		updates["zero_quota_refresh_count_delta"] = 1
	} else {
		updates["status"] = "正常"
	}
	return updates
}

type accountRefreshError struct {
	stage string
	err   error
}

func (e *accountRefreshError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *accountRefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func refreshErrorStage(err error) string {
	var refreshErr *accountRefreshError
	if errors.As(err, &refreshErr) && refreshErr.stage != "" {
		return refreshErr.stage
	}
	return "remote_info"
}

func wrapAccountRefreshError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &accountRefreshError{stage: stage, err: err}
}

func accountRefreshFailureUpdates(stage string, err error) map[string]any {
	return map[string]any{
		"status":                   "异常",
		"image_quota_unknown":      true,
		"limits_progress":          []any{},
		"restore_at":               nil,
		"quota_checked_at":         nil,
		"last_refresh_error":       err.Error(),
		"last_refresh_error_stage": stage,
		"last_refresh_error_at":    util.NowISO(),
	}
}

func (s *AccountService) recordAccountRefreshFailure(accessToken, stage string, err error) {
	if err == nil {
		return
	}
	updates := accountRefreshFailureUpdates(stage, err)
	updated := s.UpdateAccount(accessToken, updates)
	if s.logs != nil {
		logItem := map[string]any{
			"module":         "accounts",
			"operation_type": "诊断",
			"token":          util.AnonymizeToken(accessToken),
			"stage":          stage,
			"error":          err.Error(),
		}
		if updated != nil {
			logItem["status"] = updated["status"]
		}
		s.logs.Add("账号刷新验证失败", logItem)
	}
}

func (s *AccountService) RefreshAccounts(ctx context.Context, accessTokens []string) map[string]any {
	return s.refreshAccountsWithWorkerLimit(ctx, accessTokens, 10)
}

func (s *AccountService) RefreshAccountsSerial(ctx context.Context, accessTokens []string) map[string]any {
	return s.refreshAccountsWithWorkerLimit(ctx, accessTokens, 1)
}

func (s *AccountService) refreshAccountsWithWorkerLimit(ctx context.Context, accessTokens []string, workerLimit int) map[string]any {
	tokens := cleanTokens(accessTokens)
	if len(tokens) == 0 {
		return map[string]any{
			"refreshed":                 0,
			"session_refreshed":         0,
			"session_failed":            0,
			"session_validation_failed": 0,
			"errors":                    []map[string]string{},
			"results":                   []map[string]any{},
			"total":                     0,
			"failed":                    0,
			"duration_ms":               0,
			"items":                     s.ListAccounts(),
		}
	}
	startedAt := time.Now()
	pendingRefresh := []pendingRefreshItem{}
	type result struct {
		token    string
		info     map[string]any
		err      error
		duration time.Duration
	}
	workers := len(tokens)
	if workerLimit <= 0 {
		workerLimit = 1
	}
	if workers > workerLimit {
		workers = workerLimit
	}
	jobs := make(chan string)
	results := make(chan result, len(tokens))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for token := range jobs {
				started := time.Now()
				info, err := s.FetchRemoteInfo(ctx, token)
				results <- result{token: token, info: info, err: err, duration: time.Since(started)}
			}
		}()
	}
	go func() {
		for _, token := range tokens {
			jobs <- token
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	resultsByToken := make(map[string]result, len(tokens))
	for res := range results {
		resultsByToken[res.token] = res
	}

	refreshed := 0
	errors := []map[string]string{}
	details := make([]map[string]any, 0, len(tokens))
	detailsByToken := make(map[string]map[string]any, len(tokens))
	for _, token := range tokens {
		res := resultsByToken[token]
		detail := map[string]any{
			"account_id":    accountIDFromToken(token),
			"access_token":  token,
			"token_preview": util.AnonymizeToken(token),
			"success":       false,
			"status":        "error",
			"duration_ms":   res.duration.Milliseconds(),
		}
		detailsByToken[token] = detail
		if res.err == nil {
			updated := s.UpdateAccount(res.token, accountRefreshSuccessUpdates(res.info))
			if updated != nil {
				refreshed++
				detail["account_status"] = updated["status"]
				detail["email"] = updated["email"]
				detail["type"] = updated["type"]
				detail["quota"] = updated["quota"]
				detail["image_quota_unknown"] = updated["image_quota_unknown"]
				detail["restore_at"] = updated["restore_at"]
				detail["message"] = "刷新成功"
			} else {
				detail["message"] = "刷新完成，账号状态已自动处理"
			}
			detail["success"] = true
			detail["status"] = "success"
			details = append(details, detail)
			continue
		}
		message := res.err.Error()
		if normalized, handled := s.ApplyAccountError(res.token, "refresh_accounts", res.err); handled {
			message = normalized
		}
		pendingSessionRefresh := false
		if current := s.GetAccount(res.token); current != nil {
			detail["account_status"] = current["status"]
			detail["email"] = current["email"]
			detail["type"] = current["type"]
			detail["quota"] = current["quota"]
			detail["image_quota_unknown"] = current["image_quota_unknown"]
			detail["restore_at"] = current["restore_at"]
			if util.Clean(current["status"]) == "过期待刷新" {
				if st := util.Clean(current["session_token"]); st != "" {
					pendingRefresh = append(pendingRefresh, pendingRefreshItem{accessToken: res.token, sessionToken: st})
					pendingSessionRefresh = true
				}
			}
		}
		detail["message"] = message
		if pendingSessionRefresh {
			detail["status"] = "pending_session_refresh"
			details = append(details, detail)
			continue
		}
		errorItem := map[string]string{
			"account_id":   accountIDFromToken(res.token),
			"access_token": res.token,
			"error":        message,
		}
		errors = append(errors, errorItem)
		detail["error"] = message
		details = append(details, detail)
	}

	refreshedCount := 0
	failedRefreshCount := 0
	validationFailedCount := 0
	if len(pendingRefresh) > 0 {
		sortPendingRefreshByPriority(pendingRefresh, s)
	}
	for _, item := range pendingRefresh {
		detail := detailsByToken[item.accessToken]
		newAccessToken, newSessionToken, newExpires, err := s.refresher.RefreshToken(ctx, item.accessToken, item.sessionToken)
		if err != nil {
			s.recordAccountRefreshFailure(item.accessToken, "session_refresh", err)
			failedRefreshCount++
			message := fmt.Sprintf("token刷新失败: %s", err.Error())
			errors = append(errors, map[string]string{
				"account_id":   accountIDFromToken(item.accessToken),
				"access_token": item.accessToken,
				"error":        message,
			})
			if detail != nil {
				detail["status"] = "error"
				detail["message"] = message
				detail["error"] = message
				detail["account_status"] = "异常"
			}
			continue
		}
		if !s.RefreshAccountViaSession(item.accessToken, newAccessToken, newSessionToken, newExpires) {
			s.recordAccountRefreshFailure(item.accessToken, "session_refresh", fmt.Errorf("账号更新失败"))
			failedRefreshCount++
			message := "token刷新失败: 账号更新失败"
			errors = append(errors, map[string]string{
				"account_id":   accountIDFromToken(item.accessToken),
				"access_token": item.accessToken,
				"error":        message,
			})
			if detail != nil {
				detail["status"] = "error"
				detail["message"] = message
				detail["error"] = message
			}
			continue
		}
		info, validationErr := s.FetchRemoteInfo(ctx, newAccessToken)
		if validationErr != nil {
			stage := refreshErrorStage(validationErr)
			s.recordAccountRefreshFailure(newAccessToken, stage, validationErr)
			validationFailedCount++
			message := fmt.Sprintf("token刷新成功，但账号信息验证失败（阶段: %s）: %s", stage, validationErr.Error())
			errors = append(errors, map[string]string{
				"account_id":   accountIDFromToken(newAccessToken),
				"access_token": newAccessToken,
				"error":        message,
			})
			if detail != nil {
				detail["access_token"] = newAccessToken
				detail["token_preview"] = util.AnonymizeToken(newAccessToken)
				detail["status"] = "error"
				detail["message"] = "Token已刷新，但账号信息验证失败"
				detail["error"] = message
				detail["success"] = false
				if current := s.GetAccount(newAccessToken); current != nil {
					detail["account_status"] = current["status"]
					detail["email"] = current["email"]
					detail["type"] = current["type"]
					detail["quota"] = current["quota"]
					detail["image_quota_unknown"] = current["image_quota_unknown"]
					detail["restore_at"] = current["restore_at"]
				}
			}
		} else {
			s.UpdateAccount(newAccessToken, accountRefreshSuccessUpdates(info))
		}
		if detail != nil {
			detail["access_token"] = newAccessToken
			detail["token_preview"] = util.AnonymizeToken(newAccessToken)
			if validationErr == nil {
				detail["success"] = true
				detail["status"] = "success"
				detail["message"] = "Token刷新并完成账号验证"
				delete(detail, "error")
			}
			if current := s.GetAccount(newAccessToken); current != nil {
				detail["account_status"] = current["status"]
				detail["email"] = current["email"]
				detail["type"] = current["type"]
				detail["quota"] = current["quota"]
				detail["image_quota_unknown"] = current["image_quota_unknown"]
				detail["restore_at"] = current["restore_at"]
			}
		}
		refreshedCount++
	}

	return map[string]any{
		"refreshed":                 refreshed,
		"session_refreshed":         refreshedCount,
		"session_failed":            failedRefreshCount,
		"session_validation_failed": validationFailedCount,
		"errors":                    errors,
		"results":                   details,
		"total":                     len(tokens),
		"failed":                    len(errors),
		"duration_ms":               time.Since(startedAt).Milliseconds(),
		"items":                     s.ListAccounts(),
	}
}

func (s *AccountService) MarkImageResult(accessToken string, success bool) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseImageReservationLocked(accessToken)
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	next := util.CopyMap(s.items[idx])
	next["last_used_at"] = util.NowLocal()
	unknown := util.ToBool(next["image_quota_unknown"])
	if success {
		next["success"] = util.ToInt(next["success"], 0) + 1
		next["last_success_at"] = util.NowISO()
		next["zero_quota_refresh_count"] = 0
		if quota := util.ToInt(next["quota"], 0); quota > 0 {
			next["last_nonzero_quota"] = quota
		}
		if !unknown {
			quota := util.ToInt(next["quota"], 0) - 1
			if quota < 0 {
				quota = 0
			}
			next["quota"] = quota
			if quota == 0 {
				next["status"] = "限流"
				if _, ok := next["restore_at"]; !ok {
					next["restore_at"] = nil
				}
			} else if next["status"] == "限流" {
				next["status"] = "正常"
			}
		}
	} else {
		next["fail"] = util.ToInt(next["fail"], 0) + 1
	}
	account := normalizeAccount(next)
	if account == nil {
		return nil
	}
	if account["status"] == "限流" && s.config.AutoRemoveRateLimitedAccounts() {
		delete(s.imageReservations, accessToken)
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		_ = s.saveLocked()
		s.logs.Add("自动移除限流账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"token":          util.AnonymizeToken(accessToken),
		})
		return nil
	}
	s.items[idx] = account
	_ = s.saveLocked()
	return util.CopyMap(account)
}

func (s *AccountService) RemoveInvalidToken(accessToken, event string) bool {
	if !s.config.AutoRemoveInvalidAccounts() {
		return false
	}
	removed := s.RemoveToken(accessToken)
	if removed {
		s.logs.Add("自动移除异常账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"source":         event,
			"token":          util.AnonymizeToken(accessToken),
		})
	}
	return removed
}

func (s *AccountService) ApplyAccountError(accessToken, event string, err error) (string, bool) {
	if err == nil {
		return "", false
	}
	return s.ApplyAccountErrorMessage(accessToken, event, err.Error())
}

func (s *AccountService) ApplyAccountErrorMessage(accessToken, event, message string) (string, bool) {
	// The token is expired but may still be refreshable.
	if IsAccountTokenExpiredErrorMessage(message) {
		account := s.GetAccount(accessToken)
		sessionToken := ""
		if account != nil {
			sessionToken = util.Clean(account["session_token"])
		}
		if sessionToken != "" {
			// Accounts with session_token refresh asynchronously during live requests;
			// batch scans refresh serially in the second RefreshAccounts phase.
			status := "过期待刷新"
			if event != "refresh_accounts" {
				status = "刷新中"
			}
			s.UpdateAccount(accessToken, map[string]any{"status": status})
			if event != "refresh_accounts" {
				s.refreshAccountViaSessionAsync(accessToken, sessionToken)
			}
			return "检测到token过期，已提交刷新任务", true
		}
		// Accounts without session_token cannot be refreshed and become invalid.
		if !s.RemoveInvalidToken(accessToken, event) {
			s.UpdateAccount(accessToken, map[string]any{"status": "异常", "quota": 0, "image_quota_unknown": false})
		}
		return "检测到token过期且无法刷新", true
	}
	// Revoked or invalidated tokens cannot be refreshed.
	if IsAccountInvalidErrorMessage(message) {
		if !s.RemoveInvalidToken(accessToken, event) {
			s.UpdateAccount(accessToken, map[string]any{"status": "异常", "quota": 0, "image_quota_unknown": false})
		}
		return "检测到封号", true
	}
	if IsAccountRateLimitedErrorMessage(message) {
		s.UpdateAccount(accessToken, map[string]any{"status": "限流", "quota": 0, "image_quota_unknown": false})
		return "检测到限流", true
	}
	return message, false
}

// RefreshAccountViaSession updates account data after a successful session refresh.
func (s *AccountService) RefreshAccountViaSession(accessToken, newAccessToken, newSessionToken, newExpires string) bool {
	accessToken = util.Clean(accessToken)
	newAccessToken = util.Clean(newAccessToken)
	if accessToken == "" || newAccessToken == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return false
	}
	if accessToken != newAccessToken {
		if duplicateIdx := s.findIndexLocked(newAccessToken); duplicateIdx >= 0 && duplicateIdx != idx {
			s.items = append(s.items[:duplicateIdx], s.items[duplicateIdx+1:]...)
			if duplicateIdx < idx {
				idx--
			}
		}
	}

	merged := mergeMaps(s.items[idx], map[string]any{
		"access_token":             newAccessToken,
		"session_token":            newSessionToken,
		"session_expires":          newExpires,
		"status":                   "刷新中",
		"token_refreshed_at":       util.NowISO(),
		"last_refresh_error":       nil,
		"last_refresh_error_stage": nil,
		"last_refresh_error_at":    nil,
	})
	account := normalizeAccount(merged)
	if account == nil {
		return false
	}
	s.items[idx] = account
	if accessToken != newAccessToken {
		if count, ok := s.imageReservations[accessToken]; ok {
			s.imageReservations[newAccessToken] = count
			delete(s.imageReservations, accessToken)
		}
		if count, ok := s.textRequestCount[accessToken]; ok {
			s.textRequestCount[newAccessToken] = count
			delete(s.textRequestCount, accessToken)
		}
	}
	_ = s.saveLocked()
	s.logs.Add("刷新账号token", map[string]any{
		"module":         "accounts",
		"operation_type": "更新",
		"token":          util.AnonymizeToken(newAccessToken),
		"status":         account["status"],
	})
	return true
}

func (s *AccountService) refreshAccountInfoAfterSession(ctx context.Context, accessToken string) error {
	info, err := s.FetchRemoteInfo(ctx, accessToken)
	if err != nil {
		s.recordAccountRefreshFailure(accessToken, refreshErrorStage(err), err)
		return err
	}
	s.UpdateAccount(accessToken, accountRefreshSuccessUpdates(info))
	return nil
}

func (s *AccountService) FetchRemoteInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("access_token is required")
	}
	baseURL := strings.TrimRight(firstNonEmpty(s.remoteBaseURL, "https://chatgpt.com"), "/")
	headers := s.remoteHeaders(accessToken)
	client := s.browserHTTPClient(s.remoteImpersonation(accessToken), 30*time.Second)
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if err := s.bootstrapRemote(ctx, client, baseURL, accessToken); err != nil {
		return nil, wrapAccountRefreshError("bootstrap", err)
	}
	type response struct {
		payload map[string]any
		err     error
	}
	fetch := func(method, urlPath string, body any, extra map[string]string) response {
		var reader io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		}
		req, _ := http.NewRequestWithContext(ctx, method, baseURL+urlPath, reader)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("x-openai-target-path", urlPath)
		req.Header.Set("x-openai-target-route", urlPath)
		for key, value := range extra {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			return response{err: err}
		}
		defer resp.Body.Close()
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return response{err: readErr}
		}
		if resp.StatusCode != http.StatusOK {
			return response{err: refreshHTTPError(urlPath, resp.StatusCode, data)}
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return response{err: err}
		}
		return response{payload: payload}
	}
	me := fetch(http.MethodGet, "/backend-api/me", nil, nil)
	if me.err != nil {
		return nil, wrapAccountRefreshError("me", me.err)
	}
	init := fetch(http.MethodPost, "/backend-api/conversation/init", map[string]any{
		"gizmo_id": nil, "requested_default_model": nil, "conversation_id": nil, "timezone_offset_min": -480,
	}, nil)
	if init.err != nil {
		return nil, wrapAccountRefreshError("conversation_init", init.err)
	}
	limits := anyList(init.payload["limits_progress"])
	s.logs.Add("diag_limits_raw", map[string]any{
		"module":         "accounts",
		"operation_type": "诊断",
		"token":          util.AnonymizeToken(accessToken),
		"limits_len":     len(limits),
		"limits_raw":     fmt.Sprintf("%+v", init.payload["limits_progress"]),
	})
	accountType := s.detectAccountType(accessToken, me.payload, init.payload)
	quota, restoreAt, unknown := extractQuotaAndRestoreAt(limits)
	chatGPTAccountID := firstNonEmpty(
		chatGPTAccountIDFromPayload(decodeAccessTokenPayload(accessToken)),
		util.Clean(me.payload["chatgpt_account_id"]),
		util.Clean(me.payload["account_id"]),
		util.Clean(me.payload["id"]),
	)
	status := "正常"
	if !unknown && quota == 0 {
		status = "限流"
	}
	return map[string]any{
		"email":               me.payload["email"],
		"user_id":             me.payload["id"],
		"chatgpt_account_id":  chatGPTAccountID,
		"type":                accountType,
		"quota":               quota,
		"image_quota_unknown": unknown,
		"limits_progress":     limits,
		"default_model_slug":  init.payload["default_model_slug"],
		"restore_at":          restoreAt,
		"status":              status,
	}, nil
}

func (s *AccountService) bootstrapRemote(ctx context.Context, client *http.Client, baseURL, accessToken string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
	for key, value := range s.remoteBootstrapHeaders(accessToken) {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return refreshHTTPError("bootstrap", resp.StatusCode, data)
	}
	return nil
}

func (s *AccountService) StartLimitedWatcher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.reservoirMu.Lock()
				paused := s.reservoirPaused
				s.reservoirMu.Unlock()
				if paused {
					timer.Reset(interval)
					continue
				}
				tokens := s.listRefreshableLimitedTokens(time.Now())
				if len(tokens) > 0 {
					s.RefreshAccounts(ctx, tokens)
				}
				timer.Reset(interval)
			}
		}
	}()
}

func (s *AccountService) StartDailyRefreshWatcher(ctx context.Context, cfg DailyAccountRefreshConfig) {
	if cfg == nil {
		return
	}
	go func() {
		var scheduledAt time.Time
		scheduleKey := ""
		for {
			now := time.Now()
			currentKey := cfg.DailyAccountRefreshStartTime() + "-" + cfg.DailyAccountRefreshEndTime()
			if scheduledAt.IsZero() || !scheduledAt.After(now) || currentKey != scheduleKey {
				scheduledAt = nextRandomDailyRefreshTime(now, cfg.DailyAccountRefreshStartTime(), cfg.DailyAccountRefreshEndTime())
				scheduleKey = currentKey
			}
			delay := time.Until(scheduledAt)
			if delay > time.Minute {
				delay = time.Minute
			} else if delay < 0 {
				delay = 0
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			s.reservoirMu.Lock()
			paused := s.reservoirPaused
			s.reservoirMu.Unlock()
			if paused {
				continue
			}
			if !cfg.DailyAccountRefreshEnabled() {
				continue
			}
			if time.Now().Before(scheduledAt) {
				continue
			}
			s.runDailyAccountRefresh(ctx)
			scheduledAt = nextRandomDailyRefreshTime(time.Now().Add(time.Second), cfg.DailyAccountRefreshStartTime(), cfg.DailyAccountRefreshEndTime())
			scheduleKey = cfg.DailyAccountRefreshStartTime() + "-" + cfg.DailyAccountRefreshEndTime()
		}
	}()
}

func (s *AccountService) runDailyAccountRefresh(ctx context.Context) {
	tokens := s.ListTokens()
	if len(tokens) == 0 {
		return
	}
	started := time.Now()
	result := s.RefreshAccounts(ctx, tokens)
	s.logs.Add("每日自动刷新账号", map[string]any{
		"module":            "accounts",
		"operation_type":    "自动刷新",
		"total":             len(tokens),
		"refreshed":         util.ToInt(result["refreshed"], 0),
		"session_refreshed": util.ToInt(result["session_refreshed"], 0),
		"failed":            util.ToInt(result["failed"], 0),
		"duration_ms":       time.Since(started).Milliseconds(),
	})
}

func nextRandomDailyRefreshTime(now time.Time, startValue, endValue string) time.Time {
	startClock := parseDailyRefreshClock(startValue, "04:00")
	endClock := parseDailyRefreshClock(endValue, "05:00")
	windowStart := time.Date(now.Year(), now.Month(), now.Day(), startClock.Hour(), startClock.Minute(), 0, 0, now.Location())
	windowEndSameDay := time.Date(now.Year(), now.Month(), now.Day(), endClock.Hour(), endClock.Minute(), 0, 0, now.Location())
	windowEnd := windowEndSameDay
	crossesMidnight := !windowEnd.After(windowStart)
	if crossesMidnight {
		if now.Before(windowEndSameDay) {
			windowStart = windowStart.Add(-24 * time.Hour)
			windowEnd = windowEndSameDay
		} else {
			windowEnd = windowEnd.Add(24 * time.Hour)
		}
	}
	if !now.Before(windowEnd) {
		windowStart = windowStart.Add(24 * time.Hour)
		windowEnd = windowEnd.Add(24 * time.Hour)
	}
	if now.After(windowStart) {
		windowStart = now
	}
	return windowStart.Add(randomRefreshOffset(windowEnd.Sub(windowStart))).Truncate(time.Second)
}

func parseDailyRefreshClock(value, fallback string) time.Time {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err == nil {
		return parsed
	}
	parsed, _ = time.Parse("15:04", fallback)
	return parsed
}

func randomRefreshOffset(max time.Duration) time.Duration {
	maxSeconds := int64(max / time.Second)
	if maxSeconds <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(maxSeconds+1))
	if err != nil {
		return time.Duration(time.Now().UnixNano()%max.Nanoseconds()) * time.Nanosecond
	}
	return time.Duration(n.Int64()) * time.Second
}

type imageTokenReservation struct {
	token string
	slot  int
}

func (s *AccountService) reserveNextCandidateToken(excluded map[string]struct{}, allow func(map[string]any) bool) (imageTokenReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tokens []string
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := excluded[token]; ok {
			continue
		}
		if allow != nil && !allow(item) {
			continue
		}
		if isWarmingAccount(item) {
			continue
		}
		if s.availableImageSlotsLocked(item) > 0 {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		return imageTokenReservation{}, fmt.Errorf("no available image quota")
	}
	token := tokens[s.index%len(tokens)]
	s.index++
	s.ensureImageReservationsLocked()
	s.imageReservations[token]++
	return imageTokenReservation{token: token, slot: s.imageReservations[token]}, nil
}

func (s *AccountService) reservedImageSlotAvailable(reservation imageTokenReservation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(reservation.token)
	if idx < 0 {
		return false
	}
	return reservation.slot > 0 && reservation.slot <= imageAccountCapacity(s.items[idx])
}

func (s *AccountService) availableImageSlotsLocked(account map[string]any) int {
	capacity := imageAccountCapacity(account)
	if capacity <= 0 {
		return 0
	}
	token := util.Clean(account["access_token"])
	if token == "" {
		return 0
	}
	inFlight := s.imageReservations[token]
	if inFlight >= capacity {
		return 0
	}
	return capacity - inFlight
}

func (s *AccountService) releaseImageReservation(accessToken string) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseImageReservationLocked(accessToken)
}

func (s *AccountService) releaseImageReservationLocked(accessToken string) {
	s.ensureImageReservationsLocked()
	count := s.imageReservations[accessToken]
	if count <= 1 {
		delete(s.imageReservations, accessToken)
		return
	}
	s.imageReservations[accessToken] = count - 1
}

func (s *AccountService) ensureImageReservationsLocked() {
	if s.imageReservations == nil {
		s.imageReservations = map[string]int{}
	}
}

func imageAccountCapacity(account map[string]any) int {
	if !IsImageAccountAvailable(account) {
		return 0
	}
	if util.ToBool(account["image_quota_unknown"]) {
		return 1
	}
	return util.ToInt(account["quota"], 0)
}

func (s *AccountService) findIndexLocked(accessToken string) int {
	for index, item := range s.items {
		if util.Clean(item["access_token"]) == accessToken {
			return index
		}
	}
	return -1
}

func (s *AccountService) loadAccounts() []map[string]any {
	items, err := s.storage.LoadAccounts()
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if normalized := normalizeAccount(item); normalized != nil {
			out = append(out, normalized)
		}
	}
	return out
}

func (s *AccountService) saveLocked() error {
	return s.storage.SaveAccounts(s.items)
}

func (s *AccountService) remoteHeaders(accessToken string) map[string]string {
	account := s.GetAccount(accessToken)
	clean := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := account["fp"].(map[string]any); ok {
				if value := util.Clean(raw[key]); value != "" {
					return value
				}
			}
			if value := util.Clean(account[key]); value != "" {
				return value
			}
		}
		return ""
	}
	headers := map[string]string{
		"authorization":      "Bearer " + accessToken,
		"accept":             "*/*",
		"accept-language":    "zh-CN,zh;q=0.9,en;q=0.8",
		"content-type":       "application/json",
		"oai-language":       "zh-CN",
		"origin":             "https://chatgpt.com",
		"referer":            "https://chatgpt.com/",
		"sec-fetch-dest":     "empty",
		"sec-fetch-mode":     "cors",
		"sec-fetch-site":     "same-origin",
		"user-agent":         firstNonEmpty(clean("user-agent", "user_agent"), defaultRemoteUserAgent),
		"sec-ch-ua":          firstNonEmpty(clean("sec-ch-ua"), defaultRemoteSecCHUA),
		"sec-ch-ua-mobile":   firstNonEmpty(clean("sec-ch-ua-mobile"), "?0"),
		"sec-ch-ua-platform": firstNonEmpty(clean("sec-ch-ua-platform"), `"Windows"`),
	}
	if deviceID := clean("oai-device-id", "oai_device_id"); deviceID != "" {
		headers["oai-device-id"] = deviceID
	}
	if sessionID := clean("oai-session-id", "oai_session_id"); sessionID != "" {
		headers["oai-session-id"] = sessionID
	}
	return headers
}

func (s *AccountService) remoteBootstrapHeaders(accessToken string) map[string]string {
	account := s.GetAccount(accessToken)
	clean := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := account["fp"].(map[string]any); ok {
				if value := util.Clean(raw[key]); value != "" {
					return value
				}
			}
			if value := util.Clean(account[key]); value != "" {
				return value
			}
		}
		return ""
	}
	return map[string]string{
		"user-agent":                firstNonEmpty(clean("user-agent", "user_agent"), defaultRemoteUserAgent),
		"accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"accept-language":           "zh-CN,zh;q=0.9,en;q=0.8",
		"sec-ch-ua":                 firstNonEmpty(clean("sec-ch-ua"), defaultRemoteSecCHUA),
		"sec-ch-ua-mobile":          firstNonEmpty(clean("sec-ch-ua-mobile"), "?0"),
		"sec-ch-ua-platform":        firstNonEmpty(clean("sec-ch-ua-platform"), `"Windows"`),
		"sec-fetch-dest":            "document",
		"sec-fetch-mode":            "navigate",
		"sec-fetch-site":            "none",
		"sec-fetch-user":            "?1",
		"upgrade-insecure-requests": "1",
	}
}

func (s *AccountService) remoteImpersonation(accessToken string) string {
	account := s.GetAccount(accessToken)
	if raw, ok := account["fp"].(map[string]any); ok {
		if value := util.Clean(raw["impersonate"]); value != "" {
			return value
		}
	}
	return firstNonEmpty(util.Clean(account["impersonate"]), defaultRemoteProfile)
}

func (s *AccountService) detectAccountType(accessToken string, mePayload, initPayload map[string]any) string {
	tokenPayload := decodeAccessTokenPayload(accessToken)
	if authPayload, ok := tokenPayload["https://api.openai.com/auth"].(map[string]any); ok {
		if matched := normalizeAccountType(authPayload["chatgpt_plan_type"]); matched != "" {
			return matched
		}
		if matched := normalizeAccountType(authPayload["plan_type"]); matched != "" {
			return matched
		}
	}
	currentType := ""
	if current := s.GetAccount(accessToken); current != nil {
		currentType = normalizeAccountType(current["type"])
	}
	for _, payload := range []any{mePayload, initPayload, tokenPayload} {
		if matched := searchAccountType(payload); matched != "" {
			if matched == "Free" && isPaidAccountType(currentType) {
				return currentType
			}
			return matched
		}
	}
	if currentType != "" {
		return currentType
	}
	return "Free"
}

func sortPendingRefreshByPriority(items []pendingRefreshItem, s *AccountService) {
	if len(items) < 2 {
		return
	}
	paid := make([]pendingRefreshItem, 0, len(items))
	free := make([]pendingRefreshItem, 0, len(items))
	for _, item := range items {
		if isPaidRefreshAccount(s, item.accessToken) {
			paid = append(paid, item)
		} else {
			free = append(free, item)
		}
	}
	copy(items, append(paid, free...))
}

func isPaidRefreshAccount(s *AccountService, accessToken string) bool {
	account := s.GetAccount(accessToken)
	return IsPaidImageAccount(account)
}

func isWarmingAccount(account map[string]any) bool {
	return util.Clean(account["warming_status"]) == "warming"
}

// SetWarmingRunner injects the warming engine. Must be called once before
// StartWarming/StopWarming are used.
func (s *AccountService) SetWarmingRunner(runner WarmingRunner) {
	s.warmingWorker = runner
}

// StartWarming refreshes due warming accounts, then begins a warming cycle. It
// is a no-op if warming is already running or if InitWarming has not been called.
func (s *AccountService) StartWarming(ctx context.Context) map[string]any {
	if s.warmingWorker == nil {
		return nil
	}
	if s.warmingWorker.Status().Running {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	refresh := s.RefreshAccounts(ctx, s.listRefreshableWarmingTokens(time.Now()))
	s.warmingWorker.Start()
	return refresh
}

// StopWarming cancels the current warming cycle.
func (s *AccountService) StopWarming() {
	if s.warmingWorker != nil {
		s.warmingWorker.Stop()
	}
}

// WarmingStatus returns the current warming worker state.
func (s *AccountService) WarmingStatus() WarmingStatus {
	if s.warmingWorker == nil {
		return WarmingStatus{}
	}
	return s.warmingWorker.Status()
}

func IsImageAccountAvailable(account map[string]any) bool {
	if account == nil {
		return false
	}
	if isWarmingAccount(account) {
		return false
	}
	status := util.Clean(account["status"])
	if status == "禁用" || status == "限流" || status == "异常" || status == "刷新中" || status == "过期待刷新" {
		return false
	}
	if !isReservoirImageCandidate(account) {
		return false
	}
	if util.ToBool(account["image_quota_unknown"]) {
		return true
	}
	return util.ToInt(account["quota"], 0) > 0
}

func IsPaidImageAccount(account map[string]any) bool {
	switch util.Clean(account["type"]) {
	case "Plus", "ProLite", "Pro", "Team":
		return true
	default:
		return false
	}
}

func IsAccountInvalidErrorMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || isBootstrapErrorMessage(text) {
		return false
	}
	return strings.Contains(text, "token_revoked") ||
		strings.Contains(text, "invalidated oauth token")
}

// IsAccountTokenExpiredErrorMessage detects refreshable token errors.
// This includes both truly expired tokens and invalidated tokens where the
// account itself is still valid but the access_token was rotated (e.g. due
// to device context changes). When this returns true and the account has
// session_token, refresh it instead of marking the account invalid.
// Non-refreshable cases (token_revoked, invalidated oauth token) are left to
// IsAccountInvalidErrorMessage.
func IsAccountTokenExpiredErrorMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || isBootstrapErrorMessage(text) {
		return false
	}
	return strings.Contains(text, "token expired") ||
		strings.Contains(text, "authentication token is expired") ||
		strings.Contains(text, "could not parse your authentication token") ||
		strings.Contains(text, "token_invalidated") ||
		strings.Contains(text, "authentication token has been invalidated")
}

func IsAccountRateLimitedErrorMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || isBootstrapErrorMessage(text) {
		return false
	}
	if strings.Contains(text, "insufficient_quota") ||
		strings.Contains(text, "limit reached") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "image generation limit") ||
		strings.Contains(text, "you've reached") ||
		strings.Contains(text, "you have reached") ||
		strings.Contains(text, "限流") ||
		strings.Contains(text, "额度已用尽") ||
		strings.Contains(text, "生成上限") ||
		strings.Contains(text, "已达上限") {
		return true
	}
	return false
}

func normalizeAccount(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	accessToken := util.Clean(item["access_token"])
	if accessToken == "" {
		return nil
	}
	normalized := util.CopyMap(item)
	normalized["access_token"] = accessToken
	normalized["type"] = firstNonEmpty(util.Clean(normalized["type"]), "Free")
	normalized["status"] = firstNonEmpty(util.Clean(normalized["status"]), "正常")
	quota := util.ToInt(normalized["quota"], 0)
	if quota < 0 {
		quota = 0
	}
	normalized["quota"] = quota
	normalized["image_quota_unknown"] = util.ToBool(normalized["image_quota_unknown"])
	if email := util.Clean(normalized["email"]); email != "" {
		normalized["email"] = email
	} else {
		normalized["email"] = nil
	}
	if userID := util.Clean(normalized["user_id"]); userID != "" {
		normalized["user_id"] = userID
	} else {
		normalized["user_id"] = nil
	}
	if accountID := util.Clean(normalized["chatgpt_account_id"]); accountID != "" {
		normalized["chatgpt_account_id"] = accountID
	} else if accountID := util.Clean(normalized["account_id"]); accountID != "" {
		normalized["chatgpt_account_id"] = accountID
	} else {
		normalized["chatgpt_account_id"] = nil
	}
	limits := anyList(normalized["limits_progress"])
	normalized["limits_progress"] = limits
	if model := util.Clean(normalized["default_model_slug"]); model != "" {
		normalized["default_model_slug"] = model
	} else {
		normalized["default_model_slug"] = nil
	}
	if restore := util.Clean(normalized["restore_at"]); restore != "" {
		normalized["restore_at"] = restore
	} else {
		normalized["restore_at"] = nil
	}
	delete(normalized, "zero_quota_refresh_count_delta")
	normalized["success"] = util.ToInt(normalized["success"], 0)
	normalized["fail"] = util.ToInt(normalized["fail"], 0)
	normalized["last_nonzero_quota"] = util.ToInt(normalized["last_nonzero_quota"], 0)
	normalized["zero_quota_refresh_count"] = util.ToInt(normalized["zero_quota_refresh_count"], 0)
	for _, key := range []string{"quota_checked_at", "token_refreshed_at", "last_success_at", "imported_at", "refresh_cooldown_until", "last_refresh_error_at"} {
		if value := util.Clean(normalized[key]); value != "" {
			normalized[key] = value
		} else {
			normalized[key] = nil
		}
	}
	for _, key := range []string{"last_refresh_error", "last_refresh_error_stage"} {
		if value := util.Clean(normalized[key]); value != "" {
			normalized[key] = value
		} else {
			normalized[key] = nil
		}
	}
	normalized["warming_day"] = util.ToInt(normalized["warming_day"], 0)
	if warming := util.Clean(normalized["warming_status"]); warming == "warming" || warming == "done" {
		normalized["warming_status"] = warming
	} else {
		normalized["warming_status"] = nil
	}
	normalized["warming_errors"] = util.ToInt(normalized["warming_errors"], 0)
	return normalized
}

func publicAccounts(accounts []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		token := util.Clean(account["access_token"])
		if token == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":                    accountIDFromToken(token),
			"token_preview":         util.AnonymizeToken(token),
			"access_token":          token,
			"type":                  util.ValueOr(account["type"], "Free"),
			"status":                util.ValueOr(account["status"], "正常"),
			"quota":                 util.ValueOr(account["quota"], 0),
			"imageQuotaUnknown":     util.ToBool(account["image_quota_unknown"]),
			"email":                 account["email"],
			"user_id":               account["user_id"],
			"chatgpt_account_id":    account["chatgpt_account_id"],
			"limits_progress":       util.ValueOr(account["limits_progress"], []any{}),
			"default_model_slug":    account["default_model_slug"],
			"restoreAt":             account["restore_at"],
			"success":               util.ToInt(account["success"], 0),
			"fail":                  util.ToInt(account["fail"], 0),
			"lastUsedAt":            account["last_used_at"],
			"quotaCheckedAt":        account["quota_checked_at"],
			"tokenRefreshedAt":      account["token_refreshed_at"],
			"lastSuccessAt":         account["last_success_at"],
			"lastNonzeroQuota":      util.ToInt(account["last_nonzero_quota"], 0),
			"importedAt":            account["imported_at"],
			"refreshCooldownUntil":  account["refresh_cooldown_until"],
			"lastRefreshError":      account["last_refresh_error"],
			"lastRefreshErrorStage": account["last_refresh_error_stage"],
			"lastRefreshErrorAt":    account["last_refresh_error_at"],
			"zeroQuotaRefreshCount": util.ToInt(account["zero_quota_refresh_count"], 0),
			"reservoirLayer":        classifyReservoirAccount(account, time.Now(), DefaultReservoirPolicy()),
			"warmingStatus":         util.ValueOr(account["warming_status"], nil),
			"warmingDay":            util.ToInt(account["warming_day"], 0),
			"warmingErrors":         util.ToInt(account["warming_errors"], 0),
			"warmingLastActionAt":   account["warming_last_action_at"],
		})
	}
	return out
}

func accountIDFromToken(token string) string {
	return util.SHA1Short(token, 16)
}

func cleanAccountIDs(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func cleanTokens(tokens []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

// prepareAccountFP generates or preserves a persistent fingerprint for an account.
//
// Priority order:
//  1. record has internal fp → return as-is (ensure oai-device-id exists)
//  2. record has external fingerprint (CPA JSON format) → map to internal fp
//  3. nothing → auto-generate default Chrome 145 Windows fingerprint
//
// oai-device-id and oai-session-id are generated once and NEVER change,
// making each account appear as a stable, long-lived device to upstream.
func prepareAccountFP(record map[string]any) map[string]any {
	// Case 1: Internal fp already present — preserve and ensure required IDs exist
	if fp, ok := record["fp"].(map[string]any); ok {
		result := util.CopyMap(fp)
		if util.Clean(result["oai-device-id"]) == "" {
			result["oai-device-id"] = util.NewUUID()
		}
		if util.Clean(result["oai-session-id"]) == "" {
			result["oai-session-id"] = util.NewUUID()
		}
		return result
	}

	fp := map[string]any{}

	// Case 2: External CPA fingerprint format (fingerprint.browser.*)
	if raw, ok := record["fingerprint"].(map[string]any); ok {
		// Reuse external oai-device-id/session-id when available; generate only as fallback
		if v := util.Clean(raw["oai_device_id"]); v != "" {
			fp["oai-device-id"] = v
		} else {
			fp["oai-device-id"] = util.NewUUID()
		}
		if v := util.Clean(raw["oai_session_id"]); v != "" {
			fp["oai-session-id"] = v
		} else {
			fp["oai-session-id"] = util.NewUUID()
		}

		if browser, ok := raw["browser"].(map[string]any); ok {
			mapped := map[string]string{
				"user-agent":         "ua",
				"sec-ch-ua":          "sec_ch_ua",
				"sec-ch-ua-platform": "sec_ch_ua_platform",
				"sec-ch-ua-mobile":   "sec_ch_ua_mobile",
			}
			for fpKey, recordKey := range mapped {
				if v := util.Clean(browser[recordKey]); v != "" {
					fp[fpKey] = v
				}
			}
			// Derive impersonate profile from Chrome major version
			if major := util.Clean(browser["chrome_major"]); major != "" {
				fp["impersonate"] = "chrome" + major
			}
			if util.Clean(fp["impersonate"]) == "" {
				fp["impersonate"] = detectImpersonateFromUA(util.Clean(fp["user-agent"]))
			}
		}
		return fp
	}

	// Case 3: No fingerprint at all — auto-generate default
	fp["oai-device-id"] = util.NewUUID()
	fp["oai-session-id"] = util.NewUUID()
	fp["impersonate"] = "chrome133"
	fp["user-agent"] = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"
	fp["sec-ch-ua"] = `"Not:A-Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`
	fp["sec-ch-ua-mobile"] = "?0"
	fp["sec-ch-ua-platform"] = `"Windows"`
	return fp
}

// detectImpersonateFromUA extracts the impersonation profile string from a User-Agent.
// Returns "chrome{MAJOR}" for Chrome-based UAs, default "chrome133" otherwise.
func detectImpersonateFromUA(ua string) string {
	prefix := "Chrome/"
	if idx := strings.Index(ua, prefix); idx >= 0 {
		rest := ua[idx+len(prefix):]
		if end := strings.IndexAny(rest, ". "); end > 0 {
			if major := rest[:end]; major != "" {
				return "chrome" + major
			}
		}
	}
	return "chrome133"
}

func cleanAccountRecords(records []map[string]any) []map[string]any {
	seen := map[string]struct{}{}
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		token := util.Clean(record["access_token"])
		if token == "" {
			token = util.Clean(record["accessToken"])
		}
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		next := util.CopyMap(record)
		next["access_token"] = token
		out = append(out, next)
	}
	return out
}

func decodeAccessTokenPayload(accessToken string) map[string]any {
	parts := strings.Split(util.Clean(accessToken), ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	payload := parts[1]
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(payload)
	}
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return map[string]any{}
	}
	return out
}

func chatGPTAccountIDFromPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if accountID := util.Clean(payload["chatgpt_account_id"]); accountID != "" {
		return accountID
	}
	if accountID := util.Clean(payload["account_id"]); accountID != "" {
		return accountID
	}
	if authPayload := util.StringMap(payload["https://api.openai.com/auth"]); authPayload != nil {
		if accountID := util.Clean(authPayload["chatgpt_account_id"]); accountID != "" {
			return accountID
		}
	}
	return ""
}

func normalizeAccountType(value any) string {
	switch strings.ToLower(util.Clean(value)) {
	case "free":
		return "Free"
	case "plus", "personal":
		return "Plus"
	case "prolite", "pro_lite":
		return "ProLite"
	case "team", "business", "enterprise":
		return "Team"
	case "pro":
		return "Pro"
	default:
		return ""
	}
}

func importedAccountType(record map[string]any) string {
	for _, key := range []string{"type", "chatgpt_plan_type", "plan_type"} {
		if matched := normalizeAccountType(record[key]); matched != "" {
			return matched
		}
	}
	if authPayload := util.StringMap(record["https://api.openai.com/auth"]); authPayload != nil {
		for _, key := range []string{"chatgpt_plan_type", "plan_type"} {
			if matched := normalizeAccountType(authPayload[key]); matched != "" {
				return matched
			}
		}
	}
	return ""
}

func isPaidAccountType(value string) bool {
	switch normalizeAccountType(value) {
	case "Plus", "ProLite", "Pro", "Team":
		return true
	default:
		return false
	}
}

func searchAccountType(value any) string {
	switch x := value.(type) {
	case map[string]any:
		for key, item := range x {
			keyText := strings.ToLower(util.Clean(key))
			if strings.Contains(keyText, "plan") || strings.Contains(keyText, "type") || strings.Contains(keyText, "subscription") || strings.Contains(keyText, "workspace") || strings.Contains(keyText, "tier") {
				if matched := normalizeAccountType(item); matched != "" {
					return matched
				}
				if matched := searchAccountType(item); matched != "" {
					return matched
				}
			}
		}
	case []any:
		for _, item := range x {
			if matched := searchAccountType(item); matched != "" {
				return matched
			}
		}
	}
	return ""
}

func extractQuotaAndRestoreAt(limits []any) (int, any, bool) {
	for _, raw := range limits {
		item, ok := raw.(map[string]any)
		if !ok || item["feature_name"] != "image_gen" {
			continue
		}
		restore := any(nil)
		if value := util.Clean(item["reset_after"]); value != "" {
			restore = value
		}
		return util.ToInt(item["remaining"], 0), restore, false
	}
	return 0, nil, true
}

func parseAccountRestoreAt(value any) (time.Time, bool) {
	text := util.Clean(value)
	if text == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func anyList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	if list, ok := value.([]map[string]any); ok {
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = item
		}
		return out
	}
	return []any{}
}

func mergeMaps(items ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, item := range items {
		for key, value := range item {
			out[key] = value
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isBootstrapErrorMessage(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), "bootstrap failed")
}

func refreshHTTPError(context string, status int, body []byte) error {
	detail := summarizeRefreshErrorBody(body)
	if detail == "" {
		return fmt.Errorf("%s failed: HTTP %d", context, status)
	}
	return fmt.Errorf("%s failed: HTTP %d, %s", context, status, detail)
}

func summarizeRefreshErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	var payload any
	if json.Unmarshal(body, &payload) == nil {
		if detail := summarizeRefreshErrorValue(payload); detail != "" {
			return detail
		}
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare") {
		return "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy"
	}
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<body") {
		return "upstream returned HTML error page"
	}
	const maxBodyDetail = 2048
	if len(text) > maxBodyDetail {
		return "body=" + text[:maxBodyDetail] + "...(truncated)"
	}
	return "body=" + text
}

func summarizeRefreshErrorValue(value any) string {
	switch item := value.(type) {
	case map[string]any:
		for _, key := range []string{"detail", "message", "error_description"} {
			if detail := summarizeRefreshErrorValue(item[key]); detail != "" {
				return detail
			}
		}
		if detail := summarizeRefreshErrorValue(item["error"]); detail != "" {
			return detail
		}
		if data, err := json.Marshal(item); err == nil && len(data) > 0 {
			return "body=" + string(data)
		}
	case []any:
		if len(item) == 0 {
			return ""
		}
		if detail := summarizeRefreshErrorValue(item[0]); detail != "" {
			return detail
		}
	case string:
		text := strings.TrimSpace(item)
		if text != "" {
			return "body=" + text
		}
	}
	return ""
}

// shouldSkipImport checks whether the account record has is-ban or phone_invalid set to true.
func shouldSkipImport(record map[string]any) bool {
	for _, key := range []string{"is-ban", "phone_invalid", "password_invalid"} {
		if truthy(record[key]) {
			return true
		}
	}
	return false
}

func truthy(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true" || val == "1"
	case float64:
		return val != 0
	default:
		return false
	}
}

// SetImportDir configures the directory scanned by ImportScanDir.
func (s *AccountService) SetImportDir(dir string) {
	s.importDir = dir
}

// ImportScanDir is a convenience wrapper that calls ImportAccountJSONFiles
// with the directory set via SetImportDir.
func (s *AccountService) ImportScanDir() (int, map[string]string) {
	if s.importDir == "" {
		return 0, map[string]string{"": "import directory not configured"}
	}
	return s.ImportAccountJSONFiles(s.importDir)
}

// ImportAccountJSONFiles scans dir for .json files, parses each as an account
// record, imports them via AddAccountRecords, and moves successfully parsed
// files to dir/imported/ to prevent re-import.
//
// Returns the number of accounts added and a map of filename→error for files
// that could not be parsed or imported. Files that parsed correctly but were
// skipped (duplicate token) are moved to imported/ and do not appear in errors.
func (s *AccountService) ImportAccountJSONFiles(dir string) (int, map[string]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, map[string]string{"": err.Error()}
	}

	type fileRecord struct {
		path   string
		record map[string]any
	}

	var files []fileRecord
	errors := map[string]string{}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		fullPath := filepath.Join(dir, name)

		data, err := os.ReadFile(fullPath)
		if err != nil {
			errors[name] = err.Error()
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(data, &record); err != nil {
			errors[name] = err.Error()
			continue
		}
		// accept both access_token (CPA) and accessToken
		if util.Clean(record["access_token"]) == "" && util.Clean(record["accessToken"]) == "" {
			errors[name] = "missing access_token"
			continue
		}
		if shouldSkipImport(record) {
			continue
		}
		files = append(files, fileRecord{path: fullPath, record: record})
	}

	if len(files) == 0 {
		return 0, errors
	}

	records := make([]map[string]any, len(files))
	for i, f := range files {
		records[i] = f.record
	}

	result := s.AddAccountRecords(records)
	added := util.ToInt(result["added"], 0)

	// Move all processed files to imported/ so they are never scanned again.
	importedDir := filepath.Join(dir, "imported")
	_ = os.MkdirAll(importedDir, 0o755)
	for _, f := range files {
		dest := filepath.Join(importedDir, filepath.Base(f.path))
		_ = os.Rename(f.path, dest)
	}

	return added, errors
}

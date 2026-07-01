package service

import (
	"context"
	"math"
	mathrand "math/rand"
	"sort"
	"time"

	"chatgpt2api/internal/util"
)

const (
	ReservoirLayerAvailableWithQuota  = "available_with_quota"
	ReservoirLayerAvailableUnknown    = "available_unknown_quota"
	ReservoirLayerUnverifiedImported  = "unverified_imported"
	ReservoirLayerEmptyWaitingRestore = "empty_waiting_restore"
	ReservoirLayerRestoreDue          = "restore_due"
	ReservoirLayerStaleVerified       = "stale_verified"
	ReservoirLayerLongUnrefreshed     = "long_unrefreshed"
	ReservoirLayerZeroQuotaRechecked  = "zero_quota_rechecked"
	ReservoirLayerRefreshing          = "refreshing"
	ReservoirLayerInvalidOrDisabled   = "invalid_or_disabled"
)

type ReservoirPolicy struct {
	FixedMinWater        int
	LookbackWindow       time.Duration
	ProjectionWindow     time.Duration
	ForecastWindow       time.Duration
	ForecastStep         time.Duration
	ForecastSafetyWindow time.Duration
	BurstMultiplier      float64
	StaleAfter           time.Duration
	LongUnrefreshedAfter time.Duration
	ZeroQuotaMaxRechecks int
	MaxRefreshPerCycle   int
	MaintenancePerCycle  int
}

type ReservoirForecastPoint struct {
	At                 time.Time `json:"at"`
	EstimatedWater     int       `json:"estimatedWater"`
	EstimatedInflow    int       `json:"estimatedInflow"`
	EstimatedOutflow   int       `json:"estimatedOutflow"`
	RestoreDueAccounts int       `json:"restoreDueAccounts"`
	RiskLevel          string    `json:"riskLevel"`
}

type ReservoirSnapshot struct {
	Enabled              bool                     `json:"enabled"`
	Mode                 string                   `json:"mode"`
	Paused               bool                     `json:"paused"`
	Running              bool                     `json:"running"`
	CurrentWater         int                      `json:"currentWater"`
	FixedMinWater        int                      `json:"fixedMinWater"`
	TargetWater          int                      `json:"targetWater"`
	RecentOutflow10m     int                      `json:"recentOutflow10m"`
	RecentCalls10m       int                      `json:"recentCalls10m"`
	RecentSuccess10m     int                      `json:"recentSuccess10m"`
	RecentFailure10m     int                      `json:"recentFailure10m"`
	EstimatedDepletionAt *time.Time               `json:"estimatedDepletionAt,omitempty"`
	CandidateCounts      map[string]int           `json:"candidateCounts"`
	Refreshing           int                      `json:"refreshing"`
	QueueSize            int                      `json:"queueSize"`
	LastRunAt            *time.Time               `json:"lastRunAt,omitempty"`
	LastRefillAt         *time.Time               `json:"lastRefillAt,omitempty"`
	LastResult           map[string]any           `json:"lastResult,omitempty"`
	Forecast             []ReservoirForecastPoint `json:"forecast"`
	Risks                []string                 `json:"risks"`
	UpdatedAt            time.Time                `json:"updatedAt"`
}

type reservoirRefreshCandidate struct {
	token    string
	layer    string
	priority int
	age      time.Duration
	email    string
	userID   string
}

func DefaultReservoirPolicy() ReservoirPolicy {
	return ReservoirPolicy{
		FixedMinWater:        200,
		LookbackWindow:       10 * time.Minute,
		ProjectionWindow:     30 * time.Minute,
		ForecastWindow:       24 * time.Hour,
		ForecastStep:         time.Hour,
		ForecastSafetyWindow: 6 * time.Hour,
		BurstMultiplier:      3,
		StaleAfter:           6 * time.Hour,
		LongUnrefreshedAfter: 72 * time.Hour,
		ZeroQuotaMaxRechecks: 2,
		MaxRefreshPerCycle:   3,
		MaintenancePerCycle:  1,
	}
}

func (s *AccountService) ReservoirSnapshot() ReservoirSnapshot {
	policy := DefaultReservoirPolicy()
	now := time.Now()
	usage := RecentImageUsageStats{}
	if s.logs != nil {
		usage = s.logs.RecentImageUsage(policy.LookbackWindow)
	}

	s.mu.Lock()
	counts := reservoirLayerCounts()
	currentWater := 0
	for _, account := range s.items {
		layer := classifyReservoirAccount(account, now, policy)
		counts[layer]++
		if layer == ReservoirLayerAvailableWithQuota {
			currentWater += util.ToInt(account["quota"], 0)
		}
	}
	forecast := reservoirForecast(now, policy, s.items, currentWater, usage.QuotaUsed)
	queueSize := len(s.reservoirRefreshCandidatesLocked(now, policy))
	s.mu.Unlock()

	forecastGap := reservoirForecastSafetyGap(now, policy, currentWater, forecast)
	targetWater := reservoirTargetWater(policy, usage.QuotaUsed, forecastGap)
	s.reservoirMu.Lock()
	paused := s.reservoirPaused
	running := s.reservoirRunning
	refreshing := s.reservoirRefreshing
	lastRunAt := cloneTimePtr(s.reservoirLastRunAt)
	lastRefillAt := cloneTimePtr(s.reservoirLastRefillAt)
	lastResult := util.CopyMap(s.reservoirLastResult)
	s.reservoirMu.Unlock()

	mode := "maintenance"
	if paused {
		mode = "paused"
	} else if currentWater < targetWater {
		mode = "demand_refresh"
	} else if refreshing > 0 {
		mode = "refreshing"
	}

	risks := reservoirRisks(currentWater, targetWater, counts, forecast)
	return ReservoirSnapshot{
		Enabled:              true,
		Mode:                 mode,
		Paused:               paused,
		Running:              running,
		CurrentWater:         currentWater,
		FixedMinWater:        policy.FixedMinWater,
		TargetWater:          targetWater,
		RecentOutflow10m:     usage.QuotaUsed,
		RecentCalls10m:       usage.Calls,
		RecentSuccess10m:     usage.Success,
		RecentFailure10m:     usage.Failure,
		EstimatedDepletionAt: reservoirEstimatedDepletionAt(now, policy.LookbackWindow, currentWater, usage.QuotaUsed),
		CandidateCounts:      counts,
		Refreshing:           refreshing,
		QueueSize:            queueSize,
		LastRunAt:            lastRunAt,
		LastRefillAt:         lastRefillAt,
		LastResult:           lastResult,
		Forecast:             forecast,
		Risks:                risks,
		UpdatedAt:            now,
	}
}

func (s *AccountService) StartReservoirScheduler(ctx context.Context) {
	s.reservoirMu.Lock()
	if s.reservoirRunning {
		s.reservoirMu.Unlock()
		return
	}
	s.reservoirRunning = true
	s.reservoirMu.Unlock()

	go func() {
		defer func() {
			s.reservoirMu.Lock()
			s.reservoirRunning = false
			s.reservoirMu.Unlock()
		}()

		timer := time.NewTimer(reservoirSchedulerDelay())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.runReservoirCycle(ctx, false)
				timer.Reset(reservoirSchedulerDelay())
			}
		}
	}()
}

func (s *AccountService) TriggerReservoirRefill(ctx context.Context) ReservoirSnapshot {
	now := time.Now()
	s.reservoirMu.Lock()
	s.reservoirLastRefillAt = &now
	s.reservoirPaused = false
	s.reservoirMu.Unlock()
	s.runReservoirCycle(ctx, true)
	return s.ReservoirSnapshot()
}

func (s *AccountService) PauseReservoirScheduler() ReservoirSnapshot {
	s.reservoirMu.Lock()
	s.reservoirPaused = true
	s.reservoirMu.Unlock()
	return s.ReservoirSnapshot()
}

func (s *AccountService) ResumeReservoirScheduler() ReservoirSnapshot {
	s.reservoirMu.Lock()
	s.reservoirPaused = false
	s.reservoirMu.Unlock()
	return s.ReservoirSnapshot()
}

func (s *AccountService) runReservoirCycle(ctx context.Context, manual bool) map[string]any {
	if ctx == nil {
		ctx = context.Background()
	}
	s.reservoirMu.Lock()
	if s.reservoirRefreshing > 0 || (!manual && s.reservoirPaused) {
		s.reservoirMu.Unlock()
		return nil
	}
	s.reservoirRefreshing = 1
	s.reservoirMu.Unlock()

	started := time.Now()
	defer func() {
		s.reservoirMu.Lock()
		s.reservoirRefreshing = 0
		s.reservoirMu.Unlock()
	}()

	policy := DefaultReservoirPolicy()
	limit := policy.MaintenancePerCycle
	maintenance := false
	if manual {
		limit = policy.MaxRefreshPerCycle
	} else {
		snapshot := s.ReservoirSnapshot()
		if snapshot.CurrentWater < snapshot.TargetWater {
			limit = policy.MaxRefreshPerCycle
		} else {
			maintenance = true
			s.reservoirMu.Lock()
			lastMaintenance := cloneTimePtr(s.reservoirLastMaintenance)
			s.reservoirMu.Unlock()
			if lastMaintenance != nil && time.Since(*lastMaintenance) < 10*time.Minute {
				return nil
			}
		}
	}
	candidates := s.selectReservoirRefreshCandidates(time.Now(), policy, limit)
	tokens := reservoirCandidateTokens(candidates)
	result := map[string]any{
		"manual":            manual,
		"maintenance":       maintenance,
		"limit":             limit,
		"selected":          len(tokens),
		"selected_accounts": reservoirCandidateDiagnostics(candidates),
		"refreshed":         0,
		"failed":            0,
		"duration_ms":       int64(0),
	}
	if len(tokens) > 0 {
		refresh := s.RefreshAccountsSerial(ctx, tokens)
		result["refreshed"] = util.ToInt(refresh["refreshed"], 0) + util.ToInt(refresh["session_refreshed"], 0)
		result["failed"] = util.ToInt(refresh["failed"], 0)
		result["total"] = util.ToInt(refresh["total"], len(tokens))
		result["details"] = util.ValueOr(refresh["results"], []map[string]any{})
		result["errors"] = util.ValueOr(refresh["errors"], []map[string]string{})
	}
	finished := time.Now()
	result["duration_ms"] = finished.Sub(started).Milliseconds()
	if s.logs != nil {
		s.logs.Add("蓄水池调度", map[string]any{
			"module":            "accounts",
			"manual":            manual,
			"maintenance":       maintenance,
			"limit":             limit,
			"selected":          result["selected"],
			"refreshed":         result["refreshed"],
			"failed":            result["failed"],
			"duration_ms":       result["duration_ms"],
			"selected_accounts": result["selected_accounts"],
			"errors":            util.ValueOr(result["errors"], []map[string]string{}),
		})
	}

	s.reservoirMu.Lock()
	s.reservoirLastRunAt = &finished
	if maintenance && len(tokens) > 0 {
		s.reservoirLastMaintenance = &finished
	}
	s.reservoirLastResult = util.CopyMap(result)
	s.reservoirMu.Unlock()
	return result
}

func (s *AccountService) selectReservoirRefreshTokens(now time.Time, policy ReservoirPolicy, limit int) []string {
	return reservoirCandidateTokens(s.selectReservoirRefreshCandidates(now, policy, limit))
}

func (s *AccountService) selectReservoirRefreshCandidates(now time.Time, policy ReservoirPolicy, limit int) []reservoirRefreshCandidate {
	if limit <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidates := s.reservoirRefreshCandidatesLocked(now, policy)
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	for _, candidate := range candidates {
		s.lastRefreshAttempt[candidate.token] = now
	}
	return candidates
}

func reservoirCandidateTokens(candidates []reservoirRefreshCandidate) []string {
	tokens := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		tokens = append(tokens, candidate.token)
	}
	return tokens
}

func reservoirCandidateDiagnostics(candidates []reservoirRefreshCandidate) []map[string]any {
	items := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, map[string]any{
			"account_id":    accountIDFromToken(candidate.token),
			"token_preview": util.AnonymizeToken(candidate.token),
			"email":         candidate.email,
			"user_id":       candidate.userID,
			"label":         firstNonEmpty(candidate.email, candidate.userID, util.AnonymizeToken(candidate.token)),
			"layer":         candidate.layer,
			"priority":      candidate.priority,
			"age_seconds":   int64(candidate.age.Seconds()),
		})
	}
	return items
}

func (s *AccountService) reservoirRefreshCandidatesLocked(now time.Time, policy ReservoirPolicy) []reservoirRefreshCandidate {
	candidates := []reservoirRefreshCandidate{}
	for _, account := range s.items {
		token := util.Clean(account["access_token"])
		if token == "" || isReservoirRefreshCoolingDownLocked(s, token, account, now) {
			continue
		}
		layer := classifyReservoirAccount(account, now, policy)
		priority := reservoirRefreshPriority(layer)
		if priority == 0 {
			continue
		}
		verifiedAt, verified := accountVerifiedAt(account)
		age := time.Duration(0)
		if verified {
			age = now.Sub(verifiedAt)
		} else if importedAt, ok := parseAccountTime(account["imported_at"]); ok {
			age = now.Sub(importedAt)
		}
		candidates = append(candidates, reservoirRefreshCandidate{
			token:    token,
			layer:    layer,
			priority: priority,
			age:      age,
			email:    util.Clean(account["email"]),
			userID:   util.Clean(account["user_id"]),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].age > candidates[j].age
	})
	return candidates
}
func reservoirRefreshPriority(layer string) int {
	switch layer {
	case ReservoirLayerRestoreDue:
		return 500
	case ReservoirLayerUnverifiedImported:
		return 400
	case ReservoirLayerLongUnrefreshed:
		return 300
	case ReservoirLayerStaleVerified:
		return 200
	case ReservoirLayerAvailableUnknown:
		return 100
	default:
		return 0
	}
}

func isReservoirRefreshCoolingDownLocked(s *AccountService, token string, account map[string]any, now time.Time) bool {
	if cooldownUntil, ok := parseAccountTime(account["refresh_cooldown_until"]); ok && cooldownUntil.After(now) {
		return true
	}
	if last, ok := s.lastRefreshAttempt[token]; ok && now.Sub(last) < refreshCooldown {
		return true
	}
	return false
}

func reservoirSchedulerDelay() time.Duration {
	return 45*time.Second + time.Duration(mathrand.Intn(31))*time.Second
}

func reservoirLayerCounts() map[string]int {
	return map[string]int{
		ReservoirLayerAvailableWithQuota:  0,
		ReservoirLayerAvailableUnknown:    0,
		ReservoirLayerUnverifiedImported:  0,
		ReservoirLayerEmptyWaitingRestore: 0,
		ReservoirLayerRestoreDue:          0,
		ReservoirLayerStaleVerified:       0,
		ReservoirLayerLongUnrefreshed:     0,
		ReservoirLayerZeroQuotaRechecked:  0,
		ReservoirLayerRefreshing:          0,
		ReservoirLayerInvalidOrDisabled:   0,
	}
}

func classifyReservoirAccount(account map[string]any, now time.Time, policy ReservoirPolicy) string {
	if account == nil {
		return ReservoirLayerInvalidOrDisabled
	}
	status := util.Clean(account["status"])
	switch status {
	case "刷新中":
		return ReservoirLayerRefreshing
	case "禁用", "异常":
		return ReservoirLayerInvalidOrDisabled
	}
	if isWarmingAccount(account) {
		return ReservoirLayerInvalidOrDisabled
	}
	if util.ToInt(account["zero_quota_refresh_count"], 0) >= policy.ZeroQuotaMaxRechecks {
		return ReservoirLayerZeroQuotaRechecked
	}
	if status == "过期待刷新" {
		return ReservoirLayerRestoreDue
	}
	if status == "限流" {
		if restoreAt, ok := parseAccountRestoreAt(account["restore_at"]); ok && restoreAt.After(now) {
			return ReservoirLayerEmptyWaitingRestore
		}
		return ReservoirLayerRestoreDue
	}
	verifiedAt, verified := accountVerifiedAt(account)
	if !verified {
		return ReservoirLayerUnverifiedImported
	}
	quota := util.ToInt(account["quota"], 0)
	if quota > 0 {
		if now.Sub(verifiedAt) > policy.LongUnrefreshedAfter {
			return ReservoirLayerLongUnrefreshed
		}
		if now.Sub(verifiedAt) > policy.StaleAfter {
			return ReservoirLayerStaleVerified
		}
		return ReservoirLayerAvailableWithQuota
	}
	if util.ToBool(account["image_quota_unknown"]) {
		if now.Sub(verifiedAt) > policy.StaleAfter {
			return ReservoirLayerStaleVerified
		}
		return ReservoirLayerAvailableUnknown
	}
	if restoreAt, ok := parseAccountRestoreAt(account["restore_at"]); ok && restoreAt.After(now) {
		return ReservoirLayerEmptyWaitingRestore
	}
	return ReservoirLayerRestoreDue
}

func accountVerifiedAt(account map[string]any) (time.Time, bool) {
	latest := time.Time{}
	for _, key := range []string{"quota_checked_at", "last_success_at", "token_refreshed_at"} {
		if t, ok := parseAccountTime(account[key]); ok {
			if latest.IsZero() || t.After(latest) {
				latest = t
			}
		}
	}
	if util.ToInt(account["success"], 0) > 0 {
		if t, ok := parseAccountTime(account["last_used_at"]); ok {
			if latest.IsZero() || t.After(latest) {
				latest = t
			}
		}
	}
	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func isReservoirTextCandidate(account map[string]any) bool {
	layer := classifyReservoirAccount(account, time.Now(), DefaultReservoirPolicy())
	return layer == ReservoirLayerAvailableWithQuota || layer == ReservoirLayerAvailableUnknown
}

func isReservoirImageCandidate(account map[string]any) bool {
	layer := classifyReservoirAccount(account, time.Now(), DefaultReservoirPolicy())
	return layer == ReservoirLayerAvailableWithQuota || layer == ReservoirLayerAvailableUnknown
}

func reservoirTargetWater(policy ReservoirPolicy, recentOutflow, forecastGap int) int {
	dynamic := int(math.Ceil(float64(recentOutflow) * policy.BurstMultiplier))
	if dynamic < forecastGap {
		dynamic = forecastGap
	}
	if dynamic < policy.FixedMinWater {
		return policy.FixedMinWater
	}
	return dynamic
}

func reservoirForecast(now time.Time, policy ReservoirPolicy, accounts []map[string]any, currentWater, recentOutflow int) []ReservoirForecastPoint {
	if policy.ForecastWindow <= 0 || policy.ForecastStep <= 0 {
		return nil
	}
	steps := int(math.Ceil(float64(policy.ForecastWindow) / float64(policy.ForecastStep)))
	if steps <= 0 {
		return nil
	}
	outflowPerStep := reservoirForecastOutflowPerStep(policy, recentOutflow)
	points := make([]ReservoirForecastPoint, 0, steps)
	estimatedWater := currentWater
	windowStart := now
	for i := 1; i <= steps; i++ {
		windowEnd := now.Add(time.Duration(i) * policy.ForecastStep)
		inflow := 0
		restoreDueAccounts := 0
		for _, account := range accounts {
			restoreAt, ok := parseAccountRestoreAt(account["restore_at"])
			if !ok || !restoreAt.After(windowStart) || restoreAt.After(windowEnd) {
				continue
			}
			restoreDueAccounts++
			inflow += reservoirEstimatedRestoreQuota(account)
		}
		estimatedWater += inflow - outflowPerStep
		if estimatedWater < 0 {
			estimatedWater = 0
		}
		points = append(points, ReservoirForecastPoint{
			At:                 windowEnd,
			EstimatedWater:     estimatedWater,
			EstimatedInflow:    inflow,
			EstimatedOutflow:   outflowPerStep,
			RestoreDueAccounts: restoreDueAccounts,
			RiskLevel:          reservoirForecastRiskLevel(estimatedWater, policy.FixedMinWater),
		})
		windowStart = windowEnd
	}
	return points
}

func reservoirForecastOutflowPerStep(policy ReservoirPolicy, recentOutflow int) int {
	if recentOutflow <= 0 || policy.LookbackWindow <= 0 || policy.ForecastStep <= 0 {
		return 0
	}
	return int(math.Ceil(float64(recentOutflow) * (float64(policy.ForecastStep) / float64(policy.LookbackWindow))))
}

func reservoirEstimatedRestoreQuota(account map[string]any) int {
	if quota := util.ToInt(account["last_nonzero_quota"], 0); quota > 0 {
		return quota
	}
	if quota := util.ToInt(account["quota"], 0); quota > 0 {
		return quota
	}
	return 1
}

func reservoirForecastRiskLevel(estimatedWater, fixedMinWater int) string {
	if estimatedWater <= 0 {
		return "danger"
	}
	if estimatedWater < fixedMinWater {
		return "warning"
	}
	return "normal"
}

func reservoirForecastSafetyGap(now time.Time, policy ReservoirPolicy, currentWater int, forecast []ReservoirForecastPoint) int {
	if len(forecast) == 0 || policy.ForecastSafetyWindow <= 0 {
		return 0
	}
	deadline := now.Add(policy.ForecastSafetyWindow)
	maxDeficit := 0
	for _, point := range forecast {
		if point.At.After(deadline) {
			break
		}
		if deficit := policy.FixedMinWater - point.EstimatedWater; deficit > maxDeficit {
			maxDeficit = deficit
		}
	}
	if maxDeficit <= 0 {
		return 0
	}
	return currentWater + maxDeficit
}

func reservoirForecastHasRisk(forecast []ReservoirForecastPoint) bool {
	for _, point := range forecast {
		if point.RiskLevel != "normal" {
			return true
		}
	}
	return false
}
func reservoirEstimatedDepletionAt(now time.Time, window time.Duration, currentWater, recentOutflow int) *time.Time {
	if currentWater <= 0 || recentOutflow <= 0 || window <= 0 {
		return nil
	}
	duration := time.Duration(float64(window) * (float64(currentWater) / float64(recentOutflow)))
	at := now.Add(duration)
	return &at
}

func reservoirRisks(currentWater, targetWater int, counts map[string]int, forecast []ReservoirForecastPoint) []string {
	risks := []string{}
	if currentWater < targetWater {
		risks = append(risks, "当前确定水位低于目标水位")
	}
	if reservoirForecastHasRisk(forecast) {
		risks = append(risks, "未来24小时预测水位存在跌破安全线风险")
	}
	if counts[ReservoirLayerUnverifiedImported] > 0 {
		risks = append(risks, "存在未验证导入账号")
	}
	if counts[ReservoirLayerRestoreDue] > 0 {
		risks = append(risks, "存在恢复到期待刷新账号")
	}
	if counts[ReservoirLayerLongUnrefreshed] > 0 {
		risks = append(risks, "存在超过3天未刷新账号")
	}
	return risks
}

func parseAccountTime(value any) (time.Time, bool) {
	text := util.Clean(value)
	if text == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
		if parsed, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

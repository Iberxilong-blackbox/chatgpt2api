package service

import (
	"testing"
	"time"
)

func TestReservoirSnapshotClassifiesImportedAccountAsUnverified(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"imported-token"})

	snapshot := accounts.ReservoirSnapshot()
	if got := snapshot.CandidateCounts[ReservoirLayerUnverifiedImported]; got != 1 {
		t.Fatalf("unverified imported count = %d, want 1", got)
	}
	if snapshot.CurrentWater != 0 {
		t.Fatalf("current water = %d, want 0", snapshot.CurrentWater)
	}
	if token := accounts.GetTextAccessToken(); token != "" {
		t.Fatalf("GetTextAccessToken() = %q, want no unverified token", token)
	}
	if accounts.HasAvailableAccount() {
		t.Fatalf("HasAvailableAccount() = true, want false for unverified import")
	}
}

func TestReservoirVerifiedQuotaAccountParticipatesInTextAndImagePools(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"verified-token"})
	accounts.UpdateAccount("verified-token", map[string]any{
		"status":           "正常",
		"quota":            3,
		"quota_checked_at": time.Now().Format(time.RFC3339),
	})

	snapshot := accounts.ReservoirSnapshot()
	if got := snapshot.CandidateCounts[ReservoirLayerAvailableWithQuota]; got != 1 {
		t.Fatalf("available_with_quota count = %d, want 1", got)
	}
	if snapshot.CurrentWater != 3 {
		t.Fatalf("current water = %d, want 3", snapshot.CurrentWater)
	}
	if token := accounts.GetTextAccessToken(); token != "verified-token" {
		t.Fatalf("GetTextAccessToken() = %q, want verified-token", token)
	}
	if !accounts.HasAvailableAccount() {
		t.Fatalf("HasAvailableAccount() = false, want true")
	}
}

func TestReservoirSelectRefreshTokensPrioritizesDueAndUnverified(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"verified-stale", "imported-token", "restore-due"})
	now := time.Now()
	accounts.UpdateAccount("verified-stale", map[string]any{
		"status":           "正常",
		"quota":            4,
		"quota_checked_at": now.Add(-8 * time.Hour).Format(time.RFC3339),
	})
	accounts.UpdateAccount("restore-due", map[string]any{
		"status":     "限流",
		"quota":      0,
		"restore_at": now.Add(-time.Minute).Format(time.RFC3339),
	})

	tokens := accounts.selectReservoirRefreshTokens(now, DefaultReservoirPolicy(), 3)
	want := []string{"restore-due", "imported-token", "verified-stale"}
	if len(tokens) != len(want) {
		t.Fatalf("selected tokens = %#v, want %#v", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Fatalf("selected tokens = %#v, want %#v", tokens, want)
		}
	}
}

func TestReservoirSelectRefreshTokensSkipsCooldown(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"cooling-token", "ready-token"})
	now := time.Now()
	accounts.UpdateAccount("cooling-token", map[string]any{
		"status":                 "正常",
		"quota":                  0,
		"quota_checked_at":       now.Add(-time.Hour).Format(time.RFC3339),
		"image_quota_unknown":    true,
		"refresh_cooldown_until": now.Add(time.Hour).Format(time.RFC3339),
	})
	accounts.UpdateAccount("ready-token", map[string]any{
		"status":              "正常",
		"quota":               0,
		"quota_checked_at":    now.Add(-time.Hour).Format(time.RFC3339),
		"image_quota_unknown": true,
	})

	tokens := accounts.selectReservoirRefreshTokens(now, DefaultReservoirPolicy(), 2)
	if len(tokens) != 1 || tokens[0] != "ready-token" {
		t.Fatalf("selected tokens = %#v, want [ready-token]", tokens)
	}
}

func TestReservoirForecastAddsRestoreInflowFromLastNonzeroQuota(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	policy := DefaultReservoirPolicy()
	policy.ForecastWindow = 3 * time.Hour
	policy.ForecastStep = time.Hour
	accounts := []map[string]any{
		{
			"status":             "限流",
			"quota":              0,
			"restore_at":         now.Add(2 * time.Hour).Format(time.RFC3339),
			"last_nonzero_quota": 7,
		},
	}

	forecast := reservoirForecast(now, policy, accounts, 10, 0)
	if len(forecast) != 3 {
		t.Fatalf("forecast length = %d, want 3", len(forecast))
	}
	if forecast[0].EstimatedWater != 10 || forecast[0].EstimatedInflow != 0 {
		t.Fatalf("first point = %#v, want unchanged water", forecast[0])
	}
	if forecast[1].EstimatedWater != 17 || forecast[1].EstimatedInflow != 7 || forecast[1].RestoreDueAccounts != 1 {
		t.Fatalf("second point = %#v, want restore inflow 7", forecast[1])
	}
}

func TestReservoirForecastUsesConservativeRestoreQuotaFallback(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	policy := DefaultReservoirPolicy()
	policy.ForecastWindow = time.Hour
	policy.ForecastStep = time.Hour
	accounts := []map[string]any{
		{
			"status":     "限流",
			"quota":      0,
			"restore_at": now.Add(30 * time.Minute).Format(time.RFC3339),
		},
	}

	forecast := reservoirForecast(now, policy, accounts, 0, 0)
	if len(forecast) != 1 {
		t.Fatalf("forecast length = %d, want 1", len(forecast))
	}
	if forecast[0].EstimatedWater != 1 || forecast[0].EstimatedInflow != 1 {
		t.Fatalf("forecast point = %#v, want conservative inflow 1", forecast[0])
	}
}

func TestReservoirForecastSafetyGapRaisesTargetWater(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	policy := DefaultReservoirPolicy()
	policy.ForecastSafetyWindow = 6 * time.Hour
	forecast := []ReservoirForecastPoint{
		{At: now.Add(time.Hour), EstimatedWater: 180, RiskLevel: "warning"},
		{At: now.Add(2 * time.Hour), EstimatedWater: 120, RiskLevel: "warning"},
	}

	gap := reservoirForecastSafetyGap(now, policy, 220, forecast)
	if gap != 300 {
		t.Fatalf("forecast safety gap = %d, want 300", gap)
	}
	if target := reservoirTargetWater(policy, 0, gap); target != 300 {
		t.Fatalf("target water = %d, want 300", target)
	}
}

func TestReservoirSnapshotIncludesForecastRisk(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"verified-token"})
	accounts.UpdateAccount("verified-token", map[string]any{
		"status":           "正常",
		"quota":            1,
		"quota_checked_at": time.Now().Format(time.RFC3339),
	})

	snapshot := accounts.ReservoirSnapshot()
	if len(snapshot.Forecast) == 0 {
		t.Fatalf("forecast is empty")
	}
	if snapshot.Forecast[0].RiskLevel != "warning" {
		t.Fatalf("first forecast risk = %q, want warning", snapshot.Forecast[0].RiskLevel)
	}
}

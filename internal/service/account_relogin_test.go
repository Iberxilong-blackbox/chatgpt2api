package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fakeAccountReloginRunner struct {
	run func(context.Context, string, string) (AccountReloginSummary, error)
}

func (r fakeAccountReloginRunner) Run(ctx context.Context, path, runID string) (AccountReloginSummary, error) {
	return r.run(ctx, path, runID)
}

func TestAccountReloginServiceAppliesVerifiedSession(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"old-access-token"})
	accounts.UpdateAccount("old-access-token", map[string]any{"email": "user@zainy.art", "status": "异常", "quota": 3})
	imported := writeReloginArchive(t, "old-access-token", "user@zainy.art")
	runner := fakeAccountReloginRunner{run: func(_ context.Context, path, _ string) (AccountReloginSummary, error) {
		writeReloginJSON(t, path, map[string]any{
			"access_token":  "new-access-token",
			"session_token": "new-session-token",
			"expires":       "2026-08-25T00:00:00Z",
			"email":         "user@zainy.art",
			"user":          map[string]any{"id": "user-1", "name": "User"},
		})
		return AccountReloginSummary{Status: "success", Stage: "session_fetched", SourceJSONUpdated: true}, nil
	}}
	relogin := NewAccountReloginService(accounts, imported, runner)

	result := relogin.ReloginAccount(context.Background(), accountIDFromToken("old-access-token"))
	if result["success"] != true || result["stage"] != "session_fetched" {
		t.Fatalf("ReloginAccount() result = %#v", result)
	}
	if accounts.GetAccount("old-access-token") != nil {
		t.Fatal("old account still exists")
	}
	updated := accounts.GetAccount("new-access-token")
	if updated == nil || updated["session_token"] != "new-session-token" || updated["status"] != "正常" || updated["quota"] != 3 {
		t.Fatalf("updated account = %#v", updated)
	}
}

func TestAccountReloginServiceDeletesDeactivatedSnapshotAccount(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"old-access-token"})
	accounts.UpdateAccount("old-access-token", map[string]any{"email": "user@zainy.art"})
	imported := writeReloginArchive(t, "old-access-token", "user@zainy.art")
	runner := fakeAccountReloginRunner{run: func(_ context.Context, path, _ string) (AccountReloginSummary, error) {
		writeReloginJSON(t, path, map[string]any{"email": "user@zainy.art", "is-ban": true, "access_token": "", "session_token": ""})
		return AccountReloginSummary{Status: "failed", Stage: "account_deactivated", SourceDeactivationMarked: true}, nil
	}}
	relogin := NewAccountReloginService(accounts, imported, runner)

	result := relogin.ReloginAccount(context.Background(), accountIDFromToken("old-access-token"))
	if result["removed"] != true || result["stage"] != "account_deactivated" {
		t.Fatalf("ReloginAccount() result = %#v", result)
	}
	if accounts.GetAccount("old-access-token") != nil {
		t.Fatal("deactivated account was not removed")
	}
}

func TestAccountReloginServiceKeepsAccountOnNonTerminalFailure(t *testing.T) {
	accounts := newTestAccountService(t)
	accounts.AddAccounts([]string{"old-access-token"})
	accounts.UpdateAccount("old-access-token", map[string]any{"email": "user@zainy.art", "status": "正常"})
	imported := writeReloginArchive(t, "old-access-token", "user@zainy.art")
	runner := fakeAccountReloginRunner{run: func(_ context.Context, _ string, _ string) (AccountReloginSummary, error) {
		return AccountReloginSummary{Status: "failed", Stage: "otp_timeout", ErrorMessage: "OTP not received within timeout"}, nil
	}}
	relogin := NewAccountReloginService(accounts, imported, runner)

	result := relogin.ReloginAccount(context.Background(), accountIDFromToken("old-access-token"))
	if result["success"] != false || result["stage"] != "otp_timeout" {
		t.Fatalf("ReloginAccount() result = %#v", result)
	}
	account := accounts.GetAccount("old-access-token")
	if account == nil || account["status"] != "异常" || account["last_refresh_error_stage"] != "otp_timeout" {
		t.Fatalf("failed account = %#v", account)
	}
}

func writeReloginArchive(t *testing.T, accessToken, email string) string {
	t.Helper()
	imported := filepath.Join(t.TempDir(), "imported")
	if err := os.MkdirAll(imported, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(imported, "account.json")
	writeReloginJSON(t, path, map[string]any{"access_token": accessToken, "session_token": "old-session-token", "email": email, "password": "test-password"})
	return imported
}

func writeReloginJSON(t *testing.T, path string, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

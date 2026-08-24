package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/util"
)

const accountReloginTimeout = 6 * time.Minute

type AccountReloginSummary struct {
	Status                   string `json:"status"`
	Stage                    string `json:"stage"`
	ErrorMessage             string `json:"error_message"`
	SourceJSONUpdated        bool   `json:"source_json_updated"`
	SourceDeactivationMarked bool   `json:"source_deactivation_marked"`
}

type AccountReloginRunner interface {
	Run(context.Context, string, string) (AccountReloginSummary, error)
}

type CommandAccountReloginRunner struct {
	BundleDir string
	Python    string
	Display   string
}

func (r CommandAccountReloginRunner) Run(ctx context.Context, accountJSON, runID string) (AccountReloginSummary, error) {
	if r.BundleDir == "" || r.Python == "" {
		return AccountReloginSummary{}, errors.New("email relogin runner is not configured")
	}
	cmd := exec.CommandContext(ctx, r.Python, "run_single_relogin.py", "--account-json", accountJSON, "--clean-stale-cdp", "--timeout", "300", "--run-id", runID)
	cmd.Dir = r.BundleDir
	cmd.Env = append(os.Environ(), "DISPLAY="+firstNonEmpty(r.Display, ":99"))
	output, runErr := cmd.CombinedOutput() // summary.json is authoritative for both success and expected failures.
	data, err := os.ReadFile(filepath.Join(r.BundleDir, "runtime", runID, "summary.json"))
	if err != nil {
		if ctx.Err() != nil {
			return AccountReloginSummary{}, ctx.Err()
		}
		if runErr != nil {
			return AccountReloginSummary{}, fmt.Errorf("relogin runner exited before producing summary: %w (%s)", runErr, redactRunnerDiagnostic(output))
		}
		return AccountReloginSummary{}, fmt.Errorf("relogin runner did not produce summary: %w", err)
	}
	var summary AccountReloginSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return AccountReloginSummary{}, fmt.Errorf("invalid relogin summary: %w", err)
	}
	return summary, nil
}

var (
	emailDiagnosticRE  = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+`)
	secretDiagnosticRE = regexp.MustCompile(`(?i)(access[_ -]?token|session[_ -]?token|refresh[_ -]?token|id[_ -]?token|password|totp(?:[_ -]?secret)?|cookie|proxy)\s*[:=]\s*[^\s,}\]]+`)
)

func redactRunnerDiagnostic(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "no process output"
	}
	text = emailDiagnosticRE.ReplaceAllString(text, "[REDACTED_EMAIL]")
	text = secretDiagnosticRE.ReplaceAllString(text, "$1=[REDACTED]")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 240 {
		return text[:240] + "..."
	}
	return text
}

type AccountReloginService struct {
	mu       sync.Mutex
	accounts *AccountService
	imported string
	runner   AccountReloginRunner
}

func NewAccountReloginService(accounts *AccountService, imported string, runner AccountReloginRunner) *AccountReloginService {
	return &AccountReloginService{accounts: accounts, imported: imported, runner: runner}
}

func (s *AccountReloginService) ReloginAccount(ctx context.Context, accountID string) map[string]any {
	started := time.Now()
	result := map[string]any{"account_id": strings.TrimSpace(accountID), "success": false}
	oldAccessToken := s.accounts.GetTokenByID(accountID)
	if oldAccessToken == "" {
		return reloginFailure(result, "account_not_found", "account not found", started)
	}
	account := s.accounts.GetAccount(oldAccessToken)
	if account == nil {
		return reloginFailure(result, "account_not_found", "account not found", started)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner == nil {
		return s.recordFailure(oldAccessToken, result, "runner_not_configured", "email relogin runner is not configured", started)
	}
	archivePath, err := findReloginArchive(s.imported, oldAccessToken, util.Clean(account["email"]))
	if err != nil {
		return s.recordFailure(oldAccessToken, result, "archive_lookup", err.Error(), started)
	}
	runID := reloginRunID(oldAccessToken)
	runCtx, cancel := context.WithTimeout(ctx, accountReloginTimeout)
	defer cancel()
	summary, err := s.runner.Run(runCtx, archivePath, runID)
	if err != nil {
		return s.recordFailure(oldAccessToken, result, "runner", err.Error(), started)
	}
	switch summary.Stage {
	case "account_deactivated":
		removed := util.ToInt(s.accounts.DeleteAccounts([]string{oldAccessToken})["removed"], 0)
		result["removed"] = removed == 1
		result["stage"] = summary.Stage
		result["error"] = firstNonEmpty(summary.ErrorMessage, "account deactivated")
		result["duration_ms"] = time.Since(started).Milliseconds()
		return result
	}
	if summary.Status != "success" || !summary.SourceJSONUpdated {
		stage := firstNonEmpty(summary.Stage, "runner_failed")
		return s.recordFailure(oldAccessToken, result, stage, firstNonEmpty(summary.ErrorMessage, "relogin runner did not verify a new session"), started)
	}
	session, err := readReloginArchive(archivePath)
	if err != nil {
		return s.recordFailure(oldAccessToken, result, "session_read", err.Error(), started)
	}
	if _, err := s.accounts.ApplyVerifiedReloginSession(oldAccessToken, session); err != nil {
		return s.recordFailure(oldAccessToken, result, "session_apply", err.Error(), started)
	}
	result["success"] = true
	result["stage"] = firstNonEmpty(summary.Stage, "session_fetched")
	result["duration_ms"] = time.Since(started).Milliseconds()
	return result
}

func (s *AccountReloginService) recordFailure(token string, result map[string]any, stage, message string, started time.Time) map[string]any {
	s.accounts.recordAccountRefreshFailure(token, stage, errors.New(message))
	return reloginFailure(result, stage, message, started)
}

func reloginFailure(result map[string]any, stage, message string, started time.Time) map[string]any {
	result["stage"] = stage
	result["error"] = message
	result["duration_ms"] = time.Since(started).Milliseconds()
	return result
}

func reloginRunID(token string) string {
	digest := sha256.Sum256([]byte(token + time.Now().UTC().Format(time.RFC3339Nano)))
	return "go-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(digest[:4])
}

func findReloginArchive(importedDir, accessToken, email string) (string, error) {
	if importedDir == "" {
		return "", errors.New("account import directory is not configured")
	}
	var exact []string
	var emailMatches []string
	err := filepath.WalkDir(importedDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".json" {
			return walkErr
		}
		record, readErr := readReloginArchive(path)
		if readErr != nil || util.ToBool(record["is-ban"]) {
			return nil
		}
		if util.Clean(record["access_token"]) == accessToken || util.Clean(record["accessToken"]) == accessToken {
			exact = append(exact, path)
			return nil
		}
		if email != "" && strings.EqualFold(util.Clean(record["email"]), email) {
			emailMatches = append(emailMatches, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scan imported accounts: %w", err)
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 || len(emailMatches) > 1 {
		return "", errors.New("ambiguous imported account archive")
	}
	if len(emailMatches) == 1 {
		return emailMatches[0], nil
	}
	return "", errors.New("imported account archive not found")
}

func readReloginArchive(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read account archive: %w", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("parse account archive: %w", err)
	}
	return record, nil
}

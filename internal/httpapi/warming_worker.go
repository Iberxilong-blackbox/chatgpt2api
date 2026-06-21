package httpapi

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

// warmingWorker implements service.WarmingRunner.
// It lives in httpapi to avoid an import cycle between service and backend.
type warmingWorker struct {
	svc     *service.AccountService
	proxy   *service.ProxyService
	prompts []service.WarmingPrompt

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	status  service.WarmingStatus
}

// newWarmingWorker creates a warming worker and loads prompts from the given
// JSON file path. Returns an error if the prompts file cannot be read or parsed.
func newWarmingWorker(svc *service.AccountService, proxy *service.ProxyService, promptsPath string) (*warmingWorker, error) {
	prompts, err := service.LoadWarmingPrompts(promptsPath)
	if err != nil {
		return nil, err
	}
	return &warmingWorker{
		svc:     svc,
		proxy:   proxy,
		prompts: prompts,
	}, nil
}

// Start begins a warming cycle in a background goroutine. It is a no-op if
// warming is already running. Accounts whose warming_last_action_at falls on
// today are skipped so each account is processed at most once per calendar day.
func (w *warmingWorker) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.status = service.WarmingStatus{Running: true}
	w.mu.Unlock()

	go w.run(ctx)
}

// Stop cancels the current warming cycle. The running goroutine will finish
// the current account's session (no mid-session kill) and then exit.
func (w *warmingWorker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
	}
}

// Status returns a snapshot of the current warming state.
func (w *warmingWorker) Status() service.WarmingStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *warmingWorker) run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		w.running = false
		w.cancel = nil
		w.status.Running = false
		w.mu.Unlock()
	}()

	accounts := w.collectWarmingAccounts()
	if len(accounts) == 0 {
		w.updateStatus(-1, len(accounts), "")
		return
	}

	w.updateStatus(-1, len(accounts), "")

	for i, item := range accounts {
		select {
		case <-ctx.Done():
			return
		default:
		}

		token := util.Clean(item["access_token"])
		w.updateStatus(i, len(accounts), token)

		if err := w.warmAccount(ctx, item); err != nil {
			w.updateStatusError(i, len(accounts), err.Error())
			w.recordFailure(item)
			continue
		}

		w.recordSuccess(item)
	}

	w.updateStatus(len(accounts), len(accounts), "")
}

// collectWarmingAccounts returns accounts with warming_status == "warming"
// whose warming_last_action_at is not today.
func (w *warmingWorker) collectWarmingAccounts() []map[string]any {
	tokens := w.svc.ListTokens()
	out := make([]map[string]any, 0, len(tokens))
	for _, token := range tokens {
		item := w.svc.GetAccount(token)
		if util.Clean(item["warming_status"]) != "warming" {
			continue
		}
		if service.IsToday(util.Clean(item["warming_last_action_at"])) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// warmAccount runs a single warming session for one account.
//
// Flow:
//  1. Create backend.Client (uses account's stored fp — device-id/session-id stable)
//  2. Bootstrap (GET /)
//  3. Session Check (GET /api/auth/session)
//  4. Load History (GET /backend-api/conversations)
//  5. Think delay
//  6. Send question — StreamConversation, fully drain SSE
//  7. Read delay
//  8. Optional follow-up round for warming_day >= 3
func (w *warmingWorker) warmAccount(ctx context.Context, account map[string]any) error {
	token := util.Clean(account["access_token"])
	if token == "" {
		return &service.WarmingError{Msg: "missing access token"}
	}

	client := backend.NewClient(token, w.svc, w.proxy)

	// 1. Bootstrap — simulate page load (GET /)
	if err := client.Bootstrap(ctx); err != nil {
		return err
	}

	// 2. Session Check — validate token is active
	if err := client.CheckSession(ctx); err != nil {
		return err
	}

	// 3. Load History — simulate sidebar conversation list load
	// Non-fatal: continue even if this fails (some accounts may have no history)
	_ = client.LoadConversations(ctx)

	// 4. Think delay
	w.sleep(ctx, service.RandomThinkDuration())

	// 5. Send question — full SSE consumption
	prompt := w.randomPrompt()
	if err := w.sendAndConsume(ctx, client, prompt); err != nil {
		return err
	}

	// 6. Read delay
	w.sleep(ctx, service.RandomReadDuration())

	// 7. Optional follow-up round for mature accounts (day >= 3)
	day := util.ToInt(account["warming_day"], 0)
	if day >= 3 && rand.Intn(2) == 0 {
		w.sleep(ctx, service.RandomThinkDuration())
		followUp := w.randomPrompt()
		if err := w.sendAndConsume(ctx, client, followUp); err != nil {
			return err
		}
		w.sleep(ctx, service.RandomReadDuration())
	}

	return nil
}

// sendAndConsume sends a warming question and fully drains the SSE stream.
func (w *warmingWorker) sendAndConsume(ctx context.Context, client *backend.Client, prompt string) error {
	msgs, errCh := client.StreamConversation(ctx, nil, "auto", prompt)

	// Fully drain the SSE channel — must consume everything to look like a
	// real browser that reads the complete response.
	for range msgs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	// Check for stream errors after full consumption.
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (w *warmingWorker) randomPrompt() string {
	if len(w.prompts) == 0 {
		return "Hello! How are you today?"
	}
	return w.prompts[rand.Intn(len(w.prompts))].Prompt
}

func (w *warmingWorker) recordSuccess(account map[string]any) {
	token := util.Clean(account["access_token"])
	day := util.ToInt(account["warming_day"], 0)
	now := time.Now().Format(time.RFC3339)

	updates := map[string]any{
		"warming_last_action_at": now,
		"warming_errors":         0,
	}

	if day >= 6 {
		updates["warming_status"] = "done"
		updates["warming_day"] = day + 1
	} else {
		updates["warming_day"] = day + 1
	}

	w.svc.UpdateAccount(token, updates)
}

func (w *warmingWorker) recordFailure(account map[string]any) {
	token := util.Clean(account["access_token"])
	errors := util.ToInt(account["warming_errors"], 0) + 1
	w.svc.UpdateAccount(token, map[string]any{
		"warming_last_action_at": time.Now().Format(time.RFC3339),
		"warming_errors":         errors,
	})
}

func (w *warmingWorker) updateStatus(processed, total int, currentToken string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status.Processed = processed
	w.status.Total = total
	w.status.CurrentAccount = util.AnonymizeToken(currentToken)
	w.status.LastError = ""
}

func (w *warmingWorker) updateStatusError(processed, total int, errText string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status.Processed = processed
	w.status.Total = total
	w.status.LastError = errText
}

func (w *warmingWorker) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

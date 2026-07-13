package service

import (
	"time"

	"chatgpt2api/internal/util"
)

// ImageAccountSelectionEvent is safe to persist in request diagnostics.
type ImageAccountSelectionEvent struct {
	Stage              string
	AccountID          string
	StatusBefore       string
	QuotaBefore        int
	QuotaUnknownBefore bool
	LayerBefore        string
	StatusAfter        string
	QuotaAfter         int
	QuotaUnknownAfter  bool
	LayerAfter         string
	Error              string
}

func AccountIDFromToken(token string) string { return util.SHA1Short(token, 16) }

func imageAccountSelectionEvent(stage, token string, before, after map[string]any, err error) ImageAccountSelectionEvent {
	now := time.Now()
	event := ImageAccountSelectionEvent{Stage: stage, AccountID: AccountIDFromToken(token)}
	if before != nil {
		event.StatusBefore = util.Clean(before["status"])
		event.QuotaBefore = util.ToInt(before["quota"], 0)
		event.QuotaUnknownBefore = util.ToBool(before["image_quota_unknown"])
		event.LayerBefore = classifyReservoirAccount(before, now, DefaultReservoirPolicy())
	}
	if after != nil {
		event.StatusAfter = util.Clean(after["status"])
		event.QuotaAfter = util.ToInt(after["quota"], 0)
		event.QuotaUnknownAfter = util.ToBool(after["image_quota_unknown"])
		event.LayerAfter = classifyReservoirAccount(after, now, DefaultReservoirPolicy())
	}
	if err != nil {
		event.Error = err.Error()
	}
	return event
}

package service

import (
	"fmt"

	"chatgpt2api/internal/util"
)

type RegistrationIdentityImportStats struct {
	Input       int `json:"input"`
	Added       int `json:"added"`
	Duplicates  int `json:"duplicates"`
	Existing    int `json:"existing"`
	InvalidRows int `json:"invalid_rows"`
}

func MergeRegistrationIdentityIDs(raw any, identityIDs []string, label string) (map[string]any, RegistrationIdentityImportStats, error) {
	items := loadRegistrationIdentityItems(raw)
	stats := RegistrationIdentityImportStats{Input: len(identityIDs), Existing: len(items)}
	seen := map[string]struct{}{}
	for _, item := range items {
		if value := util.Clean(item["identity_id"]); value != "" {
			seen[value] = struct{}{}
		}
	}

	now := util.NowISO()
	for index, rawID := range identityIDs {
		identityID, err := normalizeRegistrationIdentityID(rawID)
		if err != nil {
			stats.InvalidRows++
			return nil, stats, fmt.Errorf("row %d: %w", index+1, err)
		}
		if _, ok := seen[identityID]; ok {
			stats.Duplicates++
			continue
		}
		seen[identityID] = struct{}{}
		items = append(items, normalizeRegistrationIdentityItem(map[string]any{
			"id":          "rid_" + util.NewHex(12),
			"identity_id": identityID,
			"label":       label,
			"enabled":     true,
			"used":        false,
			"created_at":  now,
			"updated_at":  now,
		}))
		stats.Added++
	}
	return map[string]any{"items": items}, stats, nil
}

func loadRegistrationIdentityItems(raw any) []map[string]any {
	items := util.AsMapSlice(raw)
	if obj, ok := raw.(map[string]any); ok {
		items = util.AsMapSlice(obj["items"])
	}
	out := make([]map[string]any, 0, len(items))
	seenValues := map[string]struct{}{}
	seenIDs := map[string]struct{}{}
	for _, item := range items {
		normalized := normalizeRegistrationIdentityItem(item)
		id := util.Clean(normalized["id"])
		value := util.Clean(normalized["identity_id"])
		if id == "" || value == "" {
			continue
		}
		if _, ok := seenIDs[id]; ok {
			continue
		}
		if _, ok := seenValues[value]; ok {
			continue
		}
		seenIDs[id] = struct{}{}
		seenValues[value] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

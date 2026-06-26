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

func RegistrationIdentityIDsFromDocument(raw any) []string {
	if obj, ok := raw.(map[string]any); ok {
		for _, key := range []string{"identity_ids", "ids"} {
			if ids := registrationIdentityIDsFromValue(obj[key]); len(ids) > 0 {
				return ids
			}
		}
		if ids := registrationIdentityIDsFromValue(obj["items"]); len(ids) > 0 {
			return ids
		}
		return registrationIdentityIDsFromValue(raw)
	}
	return registrationIdentityIDsFromValue(raw)
}

func registrationIdentityIDsFromValue(raw any) []string {
	switch items := raw.(type) {
	case []string:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if value := util.Clean(item); value != "" {
				out = append(out, value)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			switch value := item.(type) {
			case string:
				if text := util.Clean(value); text != "" {
					out = append(out, text)
				}
			case map[string]any:
				if text := util.Clean(value["identity_id"]); text != "" {
					out = append(out, text)
				}
			}
		}
		return out
	case []map[string]any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text := util.Clean(item["identity_id"]); text != "" {
				out = append(out, text)
			}
		}
		return out
	case map[string]any:
		if text := util.Clean(items["identity_id"]); text != "" {
			return []string{text}
		}
	}
	return nil
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

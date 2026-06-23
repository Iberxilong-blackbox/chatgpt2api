package service

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const registrationGateDocumentName = "registration_identity_ids.json"

var ErrRegistrationIdentityInvalid = errors.New("身份 ID 无效或已使用")

type RegistrationGateService struct {
	mu    sync.Mutex
	store storage.JSONDocumentBackend
	items []map[string]any
}

func NewRegistrationGateService(backend storage.Backend) *RegistrationGateService {
	s := &RegistrationGateService{store: jsonDocumentStoreFromBackend(backend)}
	s.items = s.load()
	return s
}

func (s *RegistrationGateService) List() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyMaps(s.items)
}

func (s *RegistrationGateService) Add(identityID, label string) (map[string]any, error) {
	identityID, err := normalizeRegistrationIdentityID(identityID)
	if err != nil {
		return nil, err
	}
	label = normalizeRegistrationIdentityLabel(label)
	s.mu.Lock()
	defer s.mu.Unlock()
	if registrationIdentityIndexByValueLocked(s.items, identityID) >= 0 {
		return nil, fmt.Errorf("identity ID already exists")
	}
	now := util.NowISO()
	item := normalizeRegistrationIdentityItem(map[string]any{
		"id":          "rid_" + util.NewHex(12),
		"identity_id": identityID,
		"label":       label,
		"enabled":     true,
		"used":        false,
		"created_at":  now,
		"updated_at":  now,
	})
	s.items = append(s.items, item)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return util.CopyMap(item), nil
}

func (s *RegistrationGateService) Update(id string, updates map[string]any) (map[string]any, error) {
	id = util.Clean(id)
	if id == "" {
		return nil, fmt.Errorf("identity record id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, item := range s.items {
		if util.Clean(item["id"]) != id {
			continue
		}
		next := util.CopyMap(item)
		if value, ok := updates["label"]; ok {
			next["label"] = normalizeRegistrationIdentityLabel(util.Clean(value))
		}
		if value, ok := updates["enabled"]; ok {
			next["enabled"] = util.ToBool(value)
		}
		next["updated_at"] = util.NowISO()
		s.items[index] = normalizeRegistrationIdentityItem(next)
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
		return util.CopyMap(s.items[index]), nil
	}
	return nil, fmt.Errorf("identity record not found")
}

func (s *RegistrationGateService) Delete(id string) (bool, error) {
	id = util.Clean(id)
	if id == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.items[:0]
	removed := false
	for _, item := range s.items {
		if util.Clean(item["id"]) != id {
			next = append(next, item)
			continue
		}
		if util.ToBool(item["used"]) {
			next = append(next, item)
			return false, fmt.Errorf("used identity ID cannot be deleted")
		}
		removed = true
	}
	if removed {
		s.items = next
		if err := s.saveLocked(); err != nil {
			return false, err
		}
	}
	return removed, nil
}

func (s *RegistrationGateService) ValidateAvailable(identityID string) error {
	identityID, err := normalizeRegistrationIdentityID(identityID)
	if err != nil {
		return ErrRegistrationIdentityInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	index := registrationIdentityIndexByValueLocked(s.items, identityID)
	if index < 0 {
		return ErrRegistrationIdentityInvalid
	}
	item := s.items[index]
	if !util.ToBool(util.ValueOr(item["enabled"], true)) || util.ToBool(item["used"]) {
		return ErrRegistrationIdentityInvalid
	}
	return nil
}

func (s *RegistrationGateService) Consume(identityID, userID, username string) (map[string]any, error) {
	identityID, err := normalizeRegistrationIdentityID(identityID)
	if err != nil {
		return nil, ErrRegistrationIdentityInvalid
	}
	userID = util.Clean(userID)
	username = util.Clean(username)
	if userID == "" || username == "" {
		return nil, fmt.Errorf("registered user is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if registrationIdentityIndexByUserLocked(s.items, userID) >= 0 {
		return nil, fmt.Errorf("user already has registration identity")
	}
	index := registrationIdentityIndexByValueLocked(s.items, identityID)
	if index < 0 {
		return nil, ErrRegistrationIdentityInvalid
	}
	item := s.items[index]
	if !util.ToBool(util.ValueOr(item["enabled"], true)) || util.ToBool(item["used"]) {
		return nil, ErrRegistrationIdentityInvalid
	}
	next := util.CopyMap(item)
	next["used"] = true
	next["used_by_user_id"] = userID
	next["used_by_username"] = username
	next["used_user_deleted"] = false
	now := util.NowISO()
	next["used_at"] = now
	next["updated_at"] = now
	s.items[index] = normalizeRegistrationIdentityItem(next)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return util.CopyMap(s.items[index]), nil
}

func (s *RegistrationGateService) MarkUserDeleted(userID string) error {
	userID = util.Clean(userID)
	if userID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for index, item := range s.items {
		if util.Clean(item["used_by_user_id"]) != userID {
			continue
		}
		next := util.CopyMap(item)
		next["used_user_deleted"] = true
		next["updated_at"] = util.NowISO()
		s.items[index] = normalizeRegistrationIdentityItem(next)
		changed = true
	}
	if changed {
		return s.saveLocked()
	}
	return nil
}

func (s *RegistrationGateService) UsedCountOnLocalDate(now time.Time) int {
	date := now.Local().Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, item := range s.items {
		if !util.ToBool(item["used"]) {
			continue
		}
		usedAt := util.Clean(item["used_at"])
		if registrationIdentityLocalDate(usedAt) == date {
			count++
		}
	}
	return count
}

func (s *RegistrationGateService) load() []map[string]any {
	raw := loadStoredJSON(s.store, registrationGateDocumentName)
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

func (s *RegistrationGateService) saveLocked() error {
	return saveStoredJSON(s.store, registrationGateDocumentName, map[string]any{"items": s.items})
}

func normalizeRegistrationIdentityItem(raw map[string]any) map[string]any {
	identityID, err := normalizeRegistrationIdentityID(util.Clean(raw["identity_id"]))
	if err != nil {
		identityID = ""
	}
	created := util.Clean(raw["created_at"])
	if created == "" {
		created = util.NowISO()
	}
	updated := util.Clean(raw["updated_at"])
	if updated == "" {
		updated = created
	}
	return map[string]any{
		"id":                firstNonEmpty(util.Clean(raw["id"]), "rid_"+util.NewHex(12)),
		"identity_id":       identityID,
		"label":             normalizeRegistrationIdentityLabel(util.Clean(raw["label"])),
		"enabled":           util.ToBool(util.ValueOr(raw["enabled"], true)),
		"used":              util.ToBool(raw["used"]),
		"used_by_user_id":   util.Clean(raw["used_by_user_id"]),
		"used_by_username":  util.Clean(raw["used_by_username"]),
		"used_user_deleted": util.ToBool(raw["used_user_deleted"]),
		"used_at":           util.Clean(raw["used_at"]),
		"created_at":        created,
		"updated_at":        updated,
	}
}

func normalizeRegistrationIdentityID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("identity ID is required")
	}
	if len([]rune(value)) > 256 {
		return "", fmt.Errorf("identity ID cannot exceed 256 characters")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("identity ID cannot contain control characters")
		}
	}
	return value, nil
}

func normalizeRegistrationIdentityLabel(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > 64 {
		value = string([]rune(value)[:64])
	}
	return value
}

func registrationIdentityIndexByValueLocked(items []map[string]any, identityID string) int {
	for index, item := range items {
		if util.Clean(item["identity_id"]) == identityID {
			return index
		}
	}
	return -1
}

func registrationIdentityIndexByUserLocked(items []map[string]any, userID string) int {
	for index, item := range items {
		if util.Clean(item["used_by_user_id"]) == userID {
			return index
		}
	}
	return -1
}

func registrationIdentityLocalDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.Local().Format("2006-01-02")
		}
	}
	if len(value) >= len("2006-01-02") {
		return value[:len("2006-01-02")]
	}
	return ""
}

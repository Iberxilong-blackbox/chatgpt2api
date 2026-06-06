package service

import (
	"encoding/json"
	"math/rand"
	"os"
	"strings"
	"time"
)

// WarmingPrompt represents a single warming conversation prompt.
type WarmingPrompt struct {
	Topic  string `json:"topic"`
	Prompt string `json:"prompt"`
}

// WarmingStatus reports the current state of the warming worker.
type WarmingStatus struct {
	Running        bool   `json:"running"`
	CurrentAccount string `json:"current_account,omitempty"`
	Processed      int    `json:"processed"`
	Total          int    `json:"total"`
	LastError      string `json:"last_error,omitempty"`
}

// WarmingRunner is the interface for the account warming engine.
// The concrete implementation lives in the httpapi package to avoid an
// import cycle between service and backend.
type WarmingRunner interface {
	Start()
	Stop()
	Status() WarmingStatus
}

// LoadWarmingPrompts reads and parses a warming prompts JSON file.
func LoadWarmingPrompts(path string) ([]WarmingPrompt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var prompts []WarmingPrompt
	if err := json.Unmarshal(data, &prompts); err != nil {
		return nil, err
	}
	if len(prompts) == 0 {
		return nil, &WarmingError{Msg: "warming prompts file is empty"}
	}
	return prompts, nil
}

// WarmingError is a simple error type for warming-related failures.
type WarmingError struct{ Msg string }

func (e *WarmingError) Error() string { return e.Msg }

// RandomThinkDuration returns a delay simulating a human thinking/typing.
// 90% normal (3-8s), 10% extended (15-45s).
func RandomThinkDuration() time.Duration {
	if rand.Intn(10) == 0 {
		return time.Duration(15+rand.Intn(30)) * time.Second
	}
	return time.Duration(3+rand.Intn(5)) * time.Second
}

// RandomReadDuration returns a delay simulating a human reading the response.
// 90% normal (10-20s), 10% extended (30-90s).
func RandomReadDuration() time.Duration {
	if rand.Intn(10) == 0 {
		return time.Duration(30+rand.Intn(60)) * time.Second
	}
	return time.Duration(10+rand.Intn(10)) * time.Second
}

// IsToday reports whether the given RFC3339 timestamp falls on the current
// calendar day (local time).
func IsToday(rfc3339 string) bool {
	value := strings.TrimSpace(rfc3339)
	if value == "" {
		return false
	}
	return strings.HasPrefix(value, time.Now().Format("2006-01-02"))
}

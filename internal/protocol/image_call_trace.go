package protocol

import "sync"

// ImageCallTracePayloadKey carries an in-process trace through image request bodies.
const ImageCallTracePayloadKey = "image_call_trace"

// ImageCallTrace collects safe account diagnostics for one image call.
// It deliberately never stores access or session tokens.
type ImageCallTrace struct {
	mu       sync.Mutex
	id       string
	attempts []map[string]any
}

func NewImageCallTrace(id string) *ImageCallTrace { return &ImageCallTrace{id: id} }

func ImageCallTraceFromPayload(body map[string]any) *ImageCallTrace {
	if body == nil {
		return nil
	}
	trace, _ := body[ImageCallTracePayloadKey].(*ImageCallTrace)
	return trace
}

func (t *ImageCallTrace) AddAttempt(values map[string]any) {
	if t == nil || values == nil {
		return
	}
	item := make(map[string]any, len(values))
	for key, value := range values {
		item[key] = value
	}
	t.mu.Lock()
	t.attempts = append(t.attempts, item)
	t.mu.Unlock()
}

func (t *ImageCallTrace) LogDetails() map[string]any {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	attempts := make([]map[string]any, 0, len(t.attempts))
	for _, item := range t.attempts {
		copy := make(map[string]any, len(item))
		for key, value := range item {
			copy[key] = value
		}
		attempts = append(attempts, copy)
	}
	return map[string]any{
		"trace_id":         t.id,
		"attempt_count":    len(attempts),
		"account_attempts": attempts,
	}
}

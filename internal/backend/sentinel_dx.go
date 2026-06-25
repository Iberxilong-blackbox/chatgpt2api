package backend

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"time"

	"chatgpt2api/internal/so"
)

// sosession holds the SO (Session Observer) VM state for one sentinel session.
// The collector runs asynchronously; snapshot reuses collector's register state.
type sosession struct {
	session     *so.Session
	chatToken   string // chat requirements token from finalize response
	snapshotDX  string // snapshot_dx from prepare response
	started     bool
}

// logSOEvent appends a structured JSON event line to data/logs/so_events.log.
func logSOEvent(event string, detail map[string]any) {
	os.MkdirAll("data/logs", 0755)
	f, err := os.OpenFile("data/logs/so_events.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	entry := map[string]any{
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"event":  event,
		"detail": detail,
	}
	data, _ := json.Marshal(entry)
	f.Write(append(data, '\n'))
}

// startSOCollector creates an SO session and starts the collector asynchronously.
// requirementsToken is the gAAAAAC token from sentinel/req.
// collectorDX is the so.collector_dx field from the prepare response.
func startSOCollector(requirementsToken, collectorDX, snapshotDX string) *sosession {
	if collectorDX == "" {
		log.Printf("sentinel_dx: startSOCollector — collector_dx is empty, SO disabled")
		return nil
	}
	s := &sosession{
		session:    so.NewSession(requirementsToken, collectorDX),
		snapshotDX: snapshotDX,
	}
	done := s.session.Start()
	s.started = true
	log.Printf("sentinel_dx: SO collector started — collector_dx len=%d, snapshot_dx len=%d", len(collectorDX), len(snapshotDX))
	// Fire-and-forget: don't block on done channel, but log completion
	go func() {
		<-done
		log.Printf("sentinel_dx: SO collector finished")
	}()
	return s
}

// buildSOToken runs the SO snapshot (reusing collector regs) and builds the
// openai-sentinel-so-token header value (base64 JSON).
// chatToken is the token from the finalize response.
func (s *sosession) buildSOToken(deviceID string) string {
	if s == nil || !s.started || s.snapshotDX == "" {
		return ""
	}
	soResult, err := s.session.Snapshot(s.snapshotDX)
	if err != nil {
		log.Printf("sentinel_dx: SO snapshot FAILED — %v", err)
		logSOEvent("so_snapshot_failed", map[string]any{"error": err.Error()})
		return ""
	}
	if soResult == "" {
		log.Printf("sentinel_dx: SO snapshot returned empty result")
		return ""
	}
	log.Printf("sentinel_dx: SO snapshot OK — result len=%d", len(soResult))

	soToken, err := so.BuildToken(soResult, s.chatToken, deviceID, "chatgpt")
	if err != nil {
		log.Printf("sentinel_dx: SO BuildToken FAILED — %v", err)
		return ""
	}
	log.Printf("sentinel_dx: SO token built — len=%d", len(soToken))
	return soToken
}

// setChatToken stores the chat requirements token for later SO token construction.
func (s *sosession) setChatToken(token string) {
	if s != nil {
		s.chatToken = token
	}
}

// b64decode is a diagnostic helper.
func b64decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

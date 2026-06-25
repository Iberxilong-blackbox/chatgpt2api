package backend

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"chatgpt2api/internal/prooftoken"
)

const defaultPOWScript = "https://chatgpt.com/backend-api/sentinel/sdk.js"

var scriptSrcRE = regexp.MustCompile(`(?is)<script[^>]+src=["']([^"']+)["']`)

func parsePOWResources(html string) ([]string, string) {
	matches := scriptSrcRE.FindAllStringSubmatch(html, -1)
	sources := make([]string, 0, len(matches))
	dataBuild := ""
	for _, match := range matches {
		src := match[1]
		sources = append(sources, src)
		if dataBuild == "" {
			if hit := regexp.MustCompile(`c/[^/]*/_`).FindString(src); hit != "" {
				dataBuild = hit
			}
		}
	}
	if len(sources) == 0 {
		sources = []string{defaultPOWScript}
	}
	if dataBuild == "" {
		if match := regexp.MustCompile(`<html[^>]*data-build=["']([^"']*)["']`).FindStringSubmatch(html); len(match) > 1 {
			dataBuild = match[1]
		}
	}
	return sources, dataBuild
}

// buildRequirementsToken generates a gAAAAAC-prefixed requirements token
// using the aurora 25-element fingerprint config.
func buildRequirementsToken(userAgent string) string {
	cfg := prooftoken.NewConfig(userAgent)
	return cfg.GenerateRequirementsToken()
}

// buildProofToken generates a gAAAAAB-prefixed proof token
// using the aurora 25-element fingerprint config + FNV-1a PoW.
func buildProofToken(seed, difficulty, userAgent string) (string, error) {
	cfg := prooftoken.NewConfig(userAgent)
	result := cfg.SolveProofOfWork(seed, difficulty)
	// Check if it's a fallback token (contains ErrorPrefix)
	if strings.Contains(result, prooftoken.ErrorPrefix) {
		return "", fmt.Errorf("failed to solve proof token: difficulty=%s", difficulty)
	}
	return result, nil
}

// buildSentinelReqBody constructs the JSON body for POST /sentinel/req.
func buildSentinelReqBody(p, deviceID string) string {
	body := map[string]string{"p": p, "id": deviceID, "flow": prooftoken.DefaultFlow}
	b, _ := json.Marshal(body)
	return string(b)
}

// buildSentinelTokenHeader constructs the openai-sentinel-token header value.
func buildSentinelTokenHeader(p, turnstileToken, sentinelToken, deviceID string) string {
	h := map[string]string{"p": p, "t": turnstileToken, "c": sentinelToken, "id": deviceID, "flow": prooftoken.DefaultFlow}
	b, _ := json.Marshal(h)
	return string(b)
}

// rawProofAnswer extracts the raw base64 PoW answer from a proof token.
// Format: "gAAAAAB" + <raw_answer> + "~S"  →  returns <raw_answer>.
func rawProofAnswer(proofToken string) string {
	const prefix = "gAAAAAB"
	const suffix = "~S"
	t := strings.TrimSpace(proofToken)
	if !strings.HasPrefix(t, prefix) || !strings.HasSuffix(t, suffix) {
		return t
	}
	return t[len(prefix) : len(t)-len(suffix)]
}

// zvtHash implements the FNV-1a variant used by ChatGPT's sentinel PoW verification.
// Kept for diagnostic/reference purposes.
func zvtHash(input string) string {
	h := uint32(2166136261) // FNV offset basis
	for i := 0; i < len(input); i++ {
		h ^= uint32(input[i])
		h = uint32(uint64(h) * 16777619) // FNV prime (32-bit Math.imul)
	}
	h ^= h >> 16
	h = uint32(uint64(h) * 2246822507)
	h ^= h >> 13
	h = uint32(uint64(h) * 3266489909)
	h ^= h >> 16
	return fmt.Sprintf("%08x", h)
}

// buildSentinelExtraData constructs the openai-sentinel-extra-data header value.
// Format: base64(JSON({turnstile_present, proof_present, so_present}))
// Each value is the string "true" or "false".
func buildSentinelExtraData(proofPresent, turnstilePresent, soPresent bool) string {
	boolToStr := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	data := map[string]string{
		"turnstile_present": boolToStr(turnstilePresent),
		"proof_present":     boolToStr(proofPresent),
		"so_present":        boolToStr(soPresent),
	}
	b, _ := json.Marshal(data)
	return base64.StdEncoding.EncodeToString(b)
}

// encodeConfigBase64 is a diagnostic helper for base64-encoding config arrays.
func encodeConfigBase64(config []any) string {
	b, _ := json.Marshal(config)
	return base64.StdEncoding.EncodeToString(b)
}

// powStartTime is set during Bootstrap to compute timeOrigin.
var powStartTime time.Time

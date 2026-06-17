package backend

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"time"

	"chatgpt2api/internal/util"
)

const defaultPOWScript = "https://chatgpt.com/backend-api/sentinel/sdk.js"

var (
	scriptSrcRE = regexp.MustCompile(`(?is)<script[^>]+src=["']([^"']+)["']`)
)

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

func buildLegacyRequirementsToken(userAgent string, scriptSources []string, dataBuild string, timeOrigin float64) string {
	seed := fmt.Sprintf("%f", rand.Float64())
	config := buildPOWConfig(userAgent, scriptSources, dataBuild, timeOrigin)
	answer, _ := powGenerate(seed, "0fffff", config, 500000)
	return "gAAAAAC" + answer
}

func buildProofToken(seed, difficulty, userAgent string, scriptSources []string, dataBuild string, timeOrigin float64) (string, error) {
	config := buildPOWConfig(userAgent, scriptSources, dataBuild, timeOrigin)
	answer, solved := powGenerate(seed, difficulty, config, 500000)
	if !solved {
		return "", fmt.Errorf("failed to solve proof token: difficulty=%s", difficulty)
	}
	return "gAAAAAB" + answer, nil
}

func buildPOWConfig(userAgent string, scriptSources []string, dataBuild string, timeOrigin float64) []any {
	if len(scriptSources) == 0 {
		scriptSources = []string{defaultPOWScript}
	}
	navigatorKeys := []string{
		"registerProtocolHandler−function registerProtocolHandler() { [native code] }",
		"storage−[object StorageManager]", "locks−[object LockManager]", "appCodeName−Mozilla",
		"permissions−[object Permissions]", "share−function share() { [native code] }", "webdriver−false",
		"managed−[object NavigatorManagedData]", "canShare−function canShare() { [native code] }",
		"vendor−Google Inc.", "mediaDevices−[object MediaDevices]", "vibrate−function vibrate() { [native code] }",
		"storageBuckets−[object StorageBucketManager]", "mediaCapabilities−[object MediaCapabilities]",
		"cookieEnabled−true", "virtualKeyboard−[object VirtualKeyboard]", "product−Gecko",
		"presentation−[object Presentation]", "onLine−true", "mimeTypes−[object MimeTypeArray]",
		"credentials−[object CredentialsContainer]", "serviceWorker−[object ServiceWorkerContainer]",
		"keyboard−[object Keyboard]", "gpu−[object GPU]", "doNotTrack", "serial−[object Serial]",
		"pdfViewerEnabled−true", "language−zh-CN", "geolocation−[object Geolocation]",
		"userAgentData−[object NavigatorUAData]", "getUserMedia−function getUserMedia() { [native code] }",
		"sendBeacon−function sendBeacon() { [native code] }", "hardwareConcurrency−32",
		"windowControlsOverlay−[object WindowControlsOverlay]",
	}
	windowKeys := []string{
		"0", "window", "self", "document", "name", "location", "customElements", "history", "navigation",
		"innerWidth", "innerHeight", "scrollX", "scrollY", "visualViewport", "screenX", "screenY", "outerWidth",
		"outerHeight", "devicePixelRatio", "screen", "chrome", "navigator", "onresize", "performance", "crypto",
		"indexedDB", "sessionStorage", "localStorage", "scheduler", "alert", "atob", "btoa", "fetch", "matchMedia",
		"postMessage", "queueMicrotask", "requestAnimationFrame", "setInterval", "setTimeout", "caches",
		"__NEXT_DATA__", "__BUILD_MANIFEST", "__NEXT_PRELOADREADY",
	}
	documentKeys := []string{"_reactListeningo743lnnpvdg", "location"}
	cores := []int{8, 16, 24, 32}
	now := time.Now().In(time.FixedZone("EST", -5*3600)).Format("Mon Jan 02 2006 15:04:05") + " GMT-0500 (Eastern Standard Time)"
	return []any{
		randomChoiceInt([]int{3000, 4000, 5000}),
		now,
		int64(4294705152),
		0,
		userAgent,
		randomChoice(scriptSources),
		dataBuild,
		"en-US",
		"en-US,es-US,en,es",
		0,
		randomChoice(navigatorKeys),
		randomChoice(documentKeys),
		randomChoice(windowKeys),
		float64(time.Now().UnixNano()) / 1e6,
		util.NewUUID(),
		"",
		randomChoiceInt(cores),
		timeOrigin,
		0, // Number("ai" in window)
		0, // Number("createPRNG" in window)
		0, // Number("cache" in window)
		0, // Number("data" in window)
		0, // Number("solana" in window)
		0, // Number("dump" in window)
		0, // Number("InstallTrigger" in window) — Chrome/Edge=0, Firefox=1
	}
}

func powGenerate(seed, difficulty string, config []any, limit int) (string, bool) {
	seedStr := seed
	part1 := mustMarshal(config[:3])
	part1 = append(part1[:len(part1)-1], ',')
	part2 := mustMarshal(config[4:9])
	part2 = append([]byte(","), part2[1:len(part2)-1]...)
	part2 = append(part2, ',')
	part3 := mustMarshal(config[10:])
	part3 = append([]byte(","), part3[1:]...)
	for i := 0; i < limit; i++ {
		finalJSON := bytes.Join([][]byte{
			part1,
			[]byte(fmt.Sprint(i)),
			part2,
			[]byte(fmt.Sprint(i >> 1)),
			part3,
		}, nil)
		encoded := base64.StdEncoding.EncodeToString(finalJSON)
		hashStr := zvtHash(seedStr + encoded)
		if hashStr[:len(difficulty)] <= difficulty {
			return encoded, true
		}
	}
	return randomBase64(24) + base64.StdEncoding.EncodeToString([]byte(`"`+seed+`"`)), false
}

// randomBase64 returns a random string of n base64-safe characters.
func randomBase64(n int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// zvtHash implements the FNV-1a variant used by ChatGPT's sentinel PoW verification.
// Mirrors the zvt(e) function in the sentinel SDK.
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

func mustMarshal(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

func randomChoice(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[rand.Intn(len(items))]
}

func randomChoiceInt(items []int) int {
	if len(items) == 0 {
		return 0
	}
	return items[rand.Intn(len(items))]
}

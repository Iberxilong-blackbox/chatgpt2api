package backend

import (
	"log"

	"chatgpt2api/internal/turnstile"
)

// solveTurnstileToken solves a Turnstile dx challenge using the aurora 35-opcode VM.
// requirementsToken is the gAAAAAC-prefixed token (25-element config), used as XOR key.
// dx is the base64-encrypted challenge from the prepare response.
func solveTurnstileToken(dx, requirementsToken string) string {
	if dx == "" || requirementsToken == "" {
		return ""
	}
	result, err := turnstile.SolveDX(requirementsToken, dx)
	if err != nil {
		log.Printf("turnstile: SolveDX FAILED — %v (dx len=%d, token len=%d)", err, len(dx), len(requirementsToken))
		return ""
	}
	log.Printf("turnstile: SolveDX OK — result len=%d", len(result))
	return result
}

package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// generateToken returns a random 32-byte bearer token, hex-encoded, used to
// authenticate every /v1/ request. #13 requires a per-launch token rather
// than a fixed/configurable secret, so there is nothing long-lived to leak
// beyond one `lazydeck serve` process's lifetime.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating bearer token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

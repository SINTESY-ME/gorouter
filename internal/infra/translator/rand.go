package translator

import (
	"crypto/rand"
	"encoding/hex"
)

// randHex returns a random hex string used to synthesize response ids when an
// upstream stream omits the "id" field on chat.completion.chunk events.
func randHex() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Unreachable in practice; fall back to a zero-filled id.
		return "000000000000000000000000"
	}
	return hex.EncodeToString(b)
}

package jobs

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/rs/zerolog/log"
)

// GenerateID creates a new cryptographically random job ID with the given prefix.
// The prefix should include a trailing dash, e.g. "triage-", "sel-", "enh-".
func GenerateID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal().Err(err).Msgf("Failed to generate random %s job ID", prefix)
	}
	return prefix + hex.EncodeToString(b)
}

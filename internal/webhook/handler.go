// Package webhook provides an HTTP handler for Meta/Instagram webhook
// verification and event notification processing (DDR-044).
//
// Verification (GET):
//
//	Meta sends hub.mode, hub.verify_token, and hub.challenge as query
//	parameters. The handler validates the verify token and responds with
//	the challenge value.
//
// Event Notification (POST):
//
//	Meta sends a JSON payload signed with X-Hub-Signature-256 (HMAC-SHA256
//	using the App Secret). The handler validates the signature and logs the
//	event payload for future processing.
//
// Reference: https://developers.facebook.com/docs/instagram-platform/webhooks
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/rs/zerolog/log"
)

// maxBodySize is the maximum allowed request body size (1 MB).
// Meta batches up to 1000 updates per notification, which should stay well
// under this limit.
const maxBodySize = 1 << 20 // 1 MB

// Handler handles Meta webhook verification and event notifications.
type Handler struct {
	verifyToken string
	appSecret   string
}

// NewHandler creates a webhook handler.
//
// verifyToken is a user-chosen string that must match the Verify Token
// configured in the Meta App Dashboard.
//
// appSecret is the Instagram App Secret from the Meta Developer Dashboard,
// used to validate X-Hub-Signature-256 on POST event notifications.
func NewHandler(verifyToken, appSecret string) *Handler {
	return &Handler{
		verifyToken: verifyToken,
		appSecret:   appSecret,
	}
}

// ServeHTTP dispatches to verification (GET) or event handling (POST).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleVerification(w, r)
	case http.MethodPost:
		h.handleEvent(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleVerification processes the Meta webhook verification handshake.
//
// Meta sends:
//
//	GET /webhook?hub.mode=subscribe&hub.verify_token=<token>&hub.challenge=<challenge>
//
// The handler must respond with the hub.challenge value if the verify token
// matches, or 403 if it does not.
func (h *Handler) handleVerification(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "" || challenge == "" {
		log.Warn().
			Str("mode", mode).
			Str("challenge", challenge).
			Msg("Webhook verification missing required parameters")
		http.Error(w, "missing required parameters", http.StatusBadRequest)
		return
	}

	if mode != "subscribe" {
		log.Warn().Str("mode", mode).Msg("Webhook verification unexpected mode")
		http.Error(w, "invalid mode", http.StatusBadRequest)
		return
	}

	if token != h.verifyToken {
		log.Warn().Msg("Webhook verification failed: invalid verify token")
		http.Error(w, "invalid verify token", http.StatusForbidden)
		return
	}

	log.Info().Msg("Webhook verification successful")
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(challenge))
}

// handleEvent processes incoming Meta webhook event notifications.
//
// Meta sends a POST with:
//   - JSON body containing event data
//   - X-Hub-Signature-256 header: "sha256=<hex-encoded HMAC-SHA256>"
//
// The handler validates the signature, logs the event, and responds with 200 OK.
func (h *Handler) handleEvent(w http.ResponseWriter, r *http.Request) {
	// Read body with size limit.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	if err != nil {
		log.Error().Err(err).Msg("Webhook event: failed to read body")
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		log.Warn().Msg("Webhook event: empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// Validate X-Hub-Signature-256 header.
	signature := r.Header.Get("X-Hub-Signature-256")
	if signature == "" {
		log.Warn().Msg("Webhook event: missing X-Hub-Signature-256 header")
		http.Error(w, "missing signature", http.StatusForbidden)
		return
	}

	if !h.verifySignature(body, signature) {
		log.Warn().Msg("Webhook event: invalid signature")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	// Log the event payload for future processing.
	// Using RawJSON avoids re-serialization overhead.
	log.Info().
		RawJSON("payload", body).
		Int("bodySize", len(body)).
		Msg("Webhook event received")

	w.WriteHeader(http.StatusOK)
}

// verifySignature validates the X-Hub-Signature-256 header value against
// the HMAC-SHA256 of the body using the App Secret.
//
// The header format is: "sha256=<hex-encoded hash>"
//
// Uses hmac.Equal for constant-time comparison to prevent timing attacks.
func (h *Handler) verifySignature(body []byte, header string) bool {
	// Header must start with "sha256="
	const prefix = "sha256="
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}

	receivedHex := header[len(prefix):]
	receivedBytes, err := hex.DecodeString(receivedHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.appSecret))
	mac.Write(body)
	expectedBytes := mac.Sum(nil)

	return hmac.Equal(receivedBytes, expectedBytes)
}

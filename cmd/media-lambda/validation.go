package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/store"
)

// --- Input Validation (DDR-028) ---

// uuidRegex matches UUID v4 format: 8-4-4-4-12 lowercase hex with dashes.
var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// safeFilenameRegex allows alphanumeric, dots, hyphens, underscores, spaces, and parentheses.
var safeFilenameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._ ()-]{0,254}$`)

func validateSessionID(id string) error {
	if !uuidRegex.MatchString(id) {
		return fmt.Errorf("invalid sessionId: must be a UUID (e.g., a1b2c3d4-e5f6-7890-abcd-ef1234567890)")
	}
	return nil
}

func validateFilename(name string) error {
	if name == "" {
		return fmt.Errorf("filename is required")
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("filename contains invalid characters")
	}
	if !safeFilenameRegex.MatchString(name) {
		return fmt.Errorf("filename contains invalid characters; only alphanumeric, dots, hyphens, underscores, spaces, and parentheses allowed")
	}
	return nil
}

func validateS3Key(key string) error {
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("invalid key")
	}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 || !uuidRegex.MatchString(parts[0]) || parts[1] == "" {
		return fmt.Errorf("invalid key format: expected <uuid>/<filename>")
	}
	return nil
}

// validateS3KeyBelongsToSession ensures an S3 key starts with the given sessionId prefix.
// Risk 30C: Defense-in-depth — prevents a compromised handler from accessing
// objects belonging to other sessions, even though IAM cannot restrict by
// dynamic prefix (Risk 30B limitation: sessionId is unknown at policy time).
func validateS3KeyBelongsToSession(key, sessionID string) error {
	if !strings.HasPrefix(key, sessionID+"/") {
		return fmt.Errorf("key %q does not belong to session %s", key, sessionID)
	}
	return nil
}

// --- Session Ownership Validation (Risk 15: IDOR prevention) ---

// ensureSessionOwner creates or verifies session ownership for the given sessionId.
// On the first call for a session (META record doesn't exist), creates the session
// with the authenticated user as the owner. On subsequent calls, verifies the caller
// owns the session. Returns an HTTP error and false if ownership check fails.
func ensureSessionOwner(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if sessionStore == nil {
		return true // No store configured — skip ownership check
	}

	userSub := getUserSub(r)
	if userSub == "" {
		// Unauthenticated route (e.g., thumbnail) — skip ownership check
		return true
	}

	if err := sessionStore.VerifySessionOwner(r.Context(), sessionID, userSub); err != nil {
		if strings.Contains(err.Error(), "session not found") {
			// First access — create session with owner
			session := &store.Session{
				ID:       sessionID,
				Status:   "active",
				OwnerSub: userSub,
			}
			if putErr := sessionStore.PutSession(r.Context(), session); putErr != nil {
				httpError(w, http.StatusInternalServerError, "failed to initialize session")
				return false
			}
			return true
		}
		if strings.Contains(err.Error(), "access denied") {
			httpError(w, http.StatusForbidden, "access denied")
			return false
		}
		httpError(w, http.StatusInternalServerError, "session validation failed")
		return false
	}
	return true
}

// --- Upload Validation (DDR-028) ---

// allowedContentTypes is the content-type allowlist for uploads.
var allowedContentTypes = map[string]bool{
	// Photos
	"image/jpeg":    true,
	"image/png":     true,
	"image/gif":     true,
	"image/webp":    true,
	"image/heic":    true,
	"image/heif":    true,
	"image/tiff":    true,
	"image/bmp":     true,
	"image/svg+xml": true,
	// RAW camera formats
	"image/x-adobe-dng":     true,
	"image/x-canon-cr2":     true,
	"image/x-canon-cr3":     true,
	"image/x-nikon-nef":     true,
	"image/x-sony-arw":      true,
	"image/x-fuji-raf":      true,
	"image/x-olympus-orf":   true,
	"image/x-panasonic-rw2": true,
	"image/x-samsung-srw":   true,
	// Videos
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/x-msvideo":  true,
	"video/x-matroska": true,
	"video/3gpp":       true,
	"video/MP2T":       true,
}

const maxPhotoSize int64 = 50 * 1024 * 1024       // 50 MB
const maxVideoSize int64 = 5 * 1024 * 1024 * 1024 // 5 GB

func isVideoContentType(ct string) bool {
	return strings.HasPrefix(ct, "video/")
}

package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
)

// contextKey is a typed key for request context values (Risk 15: session ownership).
type contextKey string

const userSubKey contextKey = "userSub"

// withUserIdentity extracts the Cognito `sub` claim from the API Gateway JWT
// authorizer context and stores it in the Go request context. Risk 15: IDOR prevention.
func withUserIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCtx, ok := core.GetAPIGatewayV2ContextFromContext(r.Context())
		if ok && reqCtx.Authorizer != nil && reqCtx.Authorizer.JWT != nil {
			if sub, exists := reqCtx.Authorizer.JWT.Claims["sub"]; exists && sub != "" {
				ctx := context.WithValue(r.Context(), userSubKey, sub)
				r = r.WithContext(ctx)
				log.Debug().Str("sub", sub).Str("path", r.URL.Path).Msg("User identity extracted from JWT")
			}
		}
		next.ServeHTTP(w, r)
	})
}

// getUserSub returns the authenticated user's Cognito sub from the request context.
// Returns empty string for unauthenticated routes (health, thumbnail).
func getUserSub(r *http.Request) string {
	if sub, ok := r.Context().Value(userSubKey).(string); ok {
		return sub
	}
	return ""
}

// withOriginVerify is middleware that rejects requests lacking the correct
// x-origin-verify header. CloudFront injects this header via a custom origin
// header, so direct API Gateway access is blocked. (DDR-028 Problem 1)
func withOriginVerify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Risk 5: No bypass when secret is empty — origin verification is mandatory.
		// If ORIGIN_VERIFY_SECRET is not set, all requests are blocked (fail-closed).
		if originVerifySecret == "" {
			log.Error().Str("path", r.URL.Path).Msg("Blocked request: ORIGIN_VERIFY_SECRET not configured (fail-closed)")
			httpError(w, http.StatusForbidden, "forbidden")
			return
		}
		if r.Header.Get("x-origin-verify") != originVerifySecret {
			log.Warn().Str("path", r.URL.Path).Msg("Blocked request: missing or invalid x-origin-verify header")
			httpError(w, http.StatusForbidden, "forbidden")
			return
		}
		log.Debug().Str("path", r.URL.Path).Msg("Origin verification passed")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// withMetrics is middleware that emits per-request EMF metrics, request logging,
// and X-App-Version response header (DDR-062: version identity on every response).
func withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if coldStart {
			coldStart = false
			log.Info().Str("function", "media-lambda").Str("commitHash", commitHash).Msg("Cold start — first invocation")
		}

		// DDR-062: Set version header on every response so DevTools shows the build.
		w.Header().Set("X-App-Version", commitHash)

		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(sr, r)

		elapsed := time.Since(start)

		// Normalize endpoint for dimension (avoid high cardinality from path params)
		endpoint := normalizeEndpoint(r.URL.Path)

		// DDR-062: Enhanced request logging — request ID, content-type, client version.
		log.Debug().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", sr.statusCode).
			Dur("duration", elapsed).
			Str("requestId", r.Header.Get("X-Amzn-Requestid")).
			Str("contentType", sr.Header().Get("Content-Type")).
			Str("clientVersion", r.Header.Get("X-Client-Version")).
			Msg(fmt.Sprintf("%s %s %d", r.Method, r.URL.Path, sr.statusCode))

		metrics.New("AiSocialMedia").
			Dimension("Endpoint", endpoint).
			Metric("RequestLatencyMs", float64(elapsed.Milliseconds()), metrics.UnitMilliseconds).
			Count("RequestCount").
			Property("method", r.Method).
			Property("statusCode", sr.statusCode).
			Property("path", r.URL.Path).
			Flush()
	})
}

// normalizeEndpoint maps request paths to low-cardinality endpoint names
// to avoid creating excessive CloudWatch metric dimensions.
func normalizeEndpoint(path string) string {
	switch {
	case path == "/api/health":
		return "/api/health"
	case path == "/api/upload-url":
		return "/api/upload-url"
	case path == "/api/triage/start":
		return "/api/triage/start"
	case path == "/api/selection/start":
		return "/api/selection/start"
	case path == "/api/enhance/start":
		return "/api/enhance/start"
	case path == "/api/download/start":
		return "/api/download/start"
	case path == "/api/description/generate":
		return "/api/description/generate"
	case path == "/api/session/invalidate":
		return "/api/session/invalidate"
	case path == "/api/media/thumbnail":
		return "/api/media/thumbnail"
	case path == "/api/media/full":
		return "/api/media/full"
	default:
		// Collapse parameterized routes: /api/triage/{id}/results -> /api/triage/*/results
		parts := []string{}
		for _, p := range splitPath(path) {
			if looksLikeID(p) {
				parts = append(parts, "*")
			} else {
				parts = append(parts, p)
			}
		}
		return "/" + joinPath(parts)
	}
}

func splitPath(path string) []string {
	result := []string{}
	for _, p := range split(path, '/') {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func split(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				parts = append(parts, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

func joinPath(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "/"
		}
		result += p
	}
	return result
}

// looksLikeID returns true if a path segment looks like a random ID (hex, UUID, etc.)
func looksLikeID(s string) bool {
	if len(s) < 8 {
		return false
	}
	hexCount := 0
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || c == '-' {
			hexCount++
		}
	}
	return float64(hexCount)/float64(len(s)) > 0.8
}

package main

import (
	"net/http"
	"time"

	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
)

// withOriginVerify is middleware that rejects requests lacking the correct
// x-origin-verify header. CloudFront injects this header via a custom origin
// header, so direct API Gateway access is blocked. (DDR-028 Problem 1)
func withOriginVerify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if originVerifySecret == "" {
			// Secret not configured — allow through (dev/initial deploy)
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("x-origin-verify") != originVerifySecret {
			log.Warn().Str("path", r.URL.Path).Msg("Blocked request: missing or invalid x-origin-verify header")
			httpError(w, http.StatusForbidden, "forbidden")
			return
		}
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

// withMetrics is middleware that emits per-request EMF metrics:
// RequestLatencyMs, RequestCount (with Endpoint and StatusCode dimensions).
func withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(sr, r)

		elapsed := time.Since(start)

		// Normalize endpoint for dimension (avoid high cardinality from path params)
		endpoint := normalizeEndpoint(r.URL.Path)

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

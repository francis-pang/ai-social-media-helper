package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testVerifyToken = "my_test_verify_token"
	testAppSecret   = "my_test_app_secret"
)

func newTestHandler() *Handler {
	return NewHandler(testVerifyToken, testAppSecret)
}

func signPayload(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// --- Verification (GET) Tests ---

func TestVerification_ValidToken(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet,
		"/webhook?hub.mode=subscribe&hub.verify_token="+testVerifyToken+"&hub.challenge=1158201444",
		nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "1158201444" {
		t.Errorf("expected challenge '1158201444', got '%s'", body)
	}
}

func TestVerification_InvalidToken(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet,
		"/webhook?hub.mode=subscribe&hub.verify_token=wrong_token&hub.challenge=12345",
		nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rr.Code)
	}
}

func TestVerification_MissingMode(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet,
		"/webhook?hub.verify_token="+testVerifyToken+"&hub.challenge=12345",
		nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestVerification_MissingChallenge(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet,
		"/webhook?hub.mode=subscribe&hub.verify_token="+testVerifyToken,
		nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestVerification_InvalidMode(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet,
		"/webhook?hub.mode=unsubscribe&hub.verify_token="+testVerifyToken+"&hub.challenge=12345",
		nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

// --- Event Notification (POST) Tests ---

func TestEvent_ValidSignature(t *testing.T) {
	h := newTestHandler()
	payload := `{"object":"instagram","entry":[{"id":"123","time":1520383571,"changes":[{"field":"comments","value":{"text":"hello"}}]}]}`
	sig := signPayload(testAppSecret, payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestEvent_InvalidSignature(t *testing.T) {
	h := newTestHandler()
	payload := `{"object":"instagram","entry":[]}`
	sig := signPayload("wrong_secret", payload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", sig)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rr.Code)
	}
}

func TestEvent_MissingSignature(t *testing.T) {
	h := newTestHandler()
	payload := `{"object":"instagram","entry":[]}`

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rr.Code)
	}
}

func TestEvent_EmptyBody(t *testing.T) {
	h := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(""))
	req.Header.Set("X-Hub-Signature-256", "sha256=abc123")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestEvent_MalformedSignaturePrefix(t *testing.T) {
	h := newTestHandler()
	payload := `{"object":"instagram"}`

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", "md5=abc123")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", rr.Code)
	}
}

// --- Method Tests ---

func TestMethodNotAllowed(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPut, "/webhook", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

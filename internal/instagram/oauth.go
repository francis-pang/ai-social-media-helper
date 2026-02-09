// OAuth token exchange functions for Instagram Business Login (DDR-048).
//
// Instagram uses a two-step token exchange:
//  1. Authorization code → short-lived token (1 hour) via POST to api.instagram.com
//  2. Short-lived token → long-lived token (60 days) via GET to graph.instagram.com
//
// The short-lived token response also includes the Instagram user ID.
// See: https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/business-login

package instagram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

// ExchangeCodeResult holds the response from exchanging an authorization code
// for a short-lived access token.
type ExchangeCodeResult struct {
	AccessToken string // Short-lived token (1 hour)
	UserID      string // Instagram user ID (as string)
}

// LongLivedTokenResult holds the response from exchanging a short-lived token
// for a long-lived access token.
type LongLivedTokenResult struct {
	AccessToken string // Long-lived token (60 days)
	ExpiresIn   int64  // Seconds until expiry (typically 5184000 = 60 days)
}

// shortTokenResponse is the JSON response from the Instagram token exchange endpoint.
type shortTokenResponse struct {
	AccessToken string `json:"access_token"`
	UserID      int64  `json:"user_id"`
}

// shortTokenErrorResponse is the JSON error response from the Instagram token endpoint.
type shortTokenErrorResponse struct {
	ErrorType    string `json:"error_type"`
	Code         int    `json:"code"`
	ErrorMessage string `json:"error_message"`
}

// longTokenResponse is the JSON response from the long-lived token exchange endpoint.
type longTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// ExchangeCode exchanges an Instagram authorization code for a short-lived access token.
// The authorization code comes from Meta's OAuth redirect (?code=AUTH_CODE).
//
// Endpoint: POST https://api.instagram.com/oauth/access_token
// Returns the short-lived token (1 hour) and the Instagram user ID.
func ExchangeCode(ctx context.Context, code, appID, appSecret, redirectURI string) (*ExchangeCodeResult, error) {
	params := url.Values{
		"client_id":     {appID},
		"client_secret": {appSecret},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
		"code":          {code},
	}

	log.Debug().Str("redirectUri", redirectURI).Msg("Exchanging authorization code for short-lived token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.instagram.com/oauth/access_token",
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse Instagram-specific error format.
		var errResp shortTokenErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.ErrorMessage != "" {
			return nil, fmt.Errorf("token exchange failed: %s (type: %s, code: %d)",
				errResp.ErrorMessage, errResp.ErrorType, errResp.Code)
		}
		return nil, fmt.Errorf("token exchange failed (status %d): %s",
			resp.StatusCode, truncate(string(body), 300))
	}

	var result shortTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if result.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response: %s", truncate(string(body), 300))
	}

	log.Info().Str("userId", strconv.FormatInt(result.UserID, 10)).Msg("Short-lived token obtained")

	return &ExchangeCodeResult{
		AccessToken: result.AccessToken,
		UserID:      strconv.FormatInt(result.UserID, 10),
	}, nil
}

// ExchangeLongLivedToken exchanges a short-lived Instagram token for a long-lived token.
// Long-lived tokens are valid for 60 days and can be refreshed before expiry.
//
// Endpoint: GET https://graph.instagram.com/access_token
//
//	?grant_type=ig_exchange_token
//	&client_secret={app_secret}
//	&access_token={short_lived_token}
func ExchangeLongLivedToken(ctx context.Context, shortToken, appSecret string) (*LongLivedTokenResult, error) {
	u := fmt.Sprintf("https://graph.instagram.com/access_token?grant_type=ig_exchange_token&client_secret=%s&access_token=%s",
		url.QueryEscape(appSecret), url.QueryEscape(shortToken))

	log.Debug().Msg("Exchanging short-lived token for long-lived token")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("long-lived token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("long-lived token exchange failed (status %d): %s",
			resp.StatusCode, truncate(string(body), 300))
	}

	var result longTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if result.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response: %s", truncate(string(body), 300))
	}

	days := result.ExpiresIn / 86400
	log.Info().Int64("expiresInDays", days).Msg("Long-lived token obtained")

	return &LongLivedTokenResult{
		AccessToken: result.AccessToken,
		ExpiresIn:   result.ExpiresIn,
	}, nil
}

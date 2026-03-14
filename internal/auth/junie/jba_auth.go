// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT and OAuth token storage,
// serialization, and retrieval for maintaining authenticated sessions with
// the Grazie API and JetBrains Account OAuth.
package junie

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// OAuth configuration constants for JetBrains Junie / JetBrains Account.
const (
	// AuthURL is the initial login URL for the JetBrains Account OAuth flow.
	AuthURL = "https://junie.jetbrains.com/cli-auth"
	// TokenURL is the JetBrains Account OAuth token endpoint.
	TokenURL = "https://oauth.account.jetbrains.com/oauth2/token"
	// ClientID is the OAuth client ID for the Junie CLI application.
	ClientID = "junie-cli"
	// Scope is the space-separated list of OAuth scopes requested.
	Scope = "offline_access openid jb-authn-service"
)

// jbaTokenResponse represents the JSON response from the JetBrains OAuth token endpoint.
type jbaTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// GenerateAuthURL creates the OAuth authorization URL with PKCE for JetBrains Account.
// The generated URL includes the client ID, scope, PKCE challenge, state, and redirect URI.
//
// Parameters:
//   - state: A random state parameter for CSRF protection
//   - pkceCodes: The PKCE codes for secure code exchange
//   - redirectURI: The local callback URI (e.g. http://localhost:<port>/)
//
// Returns:
//   - string: The complete authorization URL to open in a browser
//   - error: An error if PKCE codes are missing or URL generation fails
func (a *JunieAuth) GenerateAuthURL(state string, pkceCodes *PKCECodes, redirectURI string) (string, error) {
	if pkceCodes == nil {
		return "", fmt.Errorf("PKCE codes are required")
	}
	if redirectURI == "" {
		return "", fmt.Errorf("redirect URI is required")
	}

	params := url.Values{
		"client_id":      {ClientID},
		"scope":          {Scope},
		"state":          {state},
		"code_challenge": {pkceCodes.CodeChallenge},
		"redirect_uri":   {redirectURI},
	}

	authURL := fmt.Sprintf("%s?%s", AuthURL, params.Encode())
	return authURL, nil
}

// ExchangeCodeForTokens exchanges an OAuth authorization code for access and refresh tokens.
// It posts a form-encoded request to the JetBrains token endpoint following the PKCE flow.
//
// Parameters:
//   - ctx: The context for the request
//   - code: The authorization code received from the OAuth callback
//   - redirectURI: The redirect URI used in the original authorization request
//   - pkceCodes: The PKCE codes used in the original authorization request
//
// Returns:
//   - *JunieAuthBundle: The complete authentication bundle with tokens
//   - error: An error if token exchange fails
func (a *JunieAuth) ExchangeCodeForTokens(ctx context.Context, code string, redirectURI string, pkceCodes *PKCECodes) (*JunieAuthBundle, error) {
	if pkceCodes == nil {
		return nil, fmt.Errorf("PKCE codes are required for token exchange")
	}
	if code == "" {
		return nil, fmt.Errorf("authorization code is required")
	}

	formData := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {ClientID},
		"code_verifier": {pkceCodes.CodeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCodeExchangeFailed, err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("junie: failed to close token exchange response body: %v", errClose)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d: %s", ErrCodeExchangeFailed, resp.StatusCode, string(body))
	}

	var tokenResp jbaTokenResponse
	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token exchange response: %w", err)
	}

	expiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)

	tokenData := JunieTokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		Expire:       expiry,
	}

	bundle := &JunieAuthBundle{
		TokenData:   tokenData,
		LastRefresh: time.Now().Format(time.RFC3339),
	}

	return bundle, nil
}

// RefreshTokens exchanges a refresh token for a new set of access tokens.
// It posts a form-encoded request to the JetBrains token endpoint.
//
// Parameters:
//   - ctx: The context for the request
//   - refreshToken: The refresh token to exchange for new tokens
//
// Returns:
//   - *JunieTokenData: The new token data with updated access token
//   - error: An error if token refresh fails
func (a *JunieAuth) RefreshTokens(ctx context.Context, refreshToken string) (*JunieTokenData, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is required")
	}

	formData := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp jbaTokenResponse
	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token refresh response: %w", err)
	}

	expiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)

	return &JunieTokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		Expire:       expiry,
	}, nil
}

// RefreshTokensWithRetry refreshes tokens with automatic linear-backoff retry logic.
// Each retry waits attempt * time.Second before retrying.
//
// Parameters:
//   - ctx: The context for the request
//   - refreshToken: The refresh token to use
//   - maxRetries: The maximum number of retry attempts
//
// Returns:
//   - *JunieTokenData: The refreshed token data
//   - error: An error if all retry attempts fail
func (a *JunieAuth) RefreshTokensWithRetry(ctx context.Context, refreshToken string, maxRetries int) (*JunieTokenData, error) {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Linear backoff: wait attempt * 1 second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		tokenData, err := a.RefreshTokens(ctx, refreshToken)
		if err == nil {
			return tokenData, nil
		}

		lastErr = err
		log.Warnf("junie: token refresh attempt %d failed: %v", attempt+1, err)
	}

	return nil, fmt.Errorf("token refresh failed after %d attempts: %w", maxRetries, lastErr)
}

// CreateTokenStorage creates a new JunieTokenStorage from an auth bundle.
// This method is on JunieAuth to follow the same pattern as other providers.
//
// Parameters:
//   - bundle: The authentication bundle containing token data
//
// Returns:
//   - *JunieTokenStorage: A new token storage instance ready to be saved
func (a *JunieAuth) CreateTokenStorage(bundle *JunieAuthBundle) *JunieTokenStorage {
	return &JunieTokenStorage{
		AccessToken:  bundle.TokenData.AccessToken,
		RefreshToken: bundle.TokenData.RefreshToken,
		IDToken:      bundle.TokenData.IDToken,
		Expire:       bundle.TokenData.Expire,
		LastRefresh:  bundle.LastRefresh,
	}
}

// UpdateTokenStorage updates an existing JunieTokenStorage with newly refreshed token data.
//
// Parameters:
//   - storage: The existing token storage to update in place
//   - tokenData: The new token data to apply
func (a *JunieAuth) UpdateTokenStorage(storage *JunieTokenStorage, tokenData *JunieTokenData) {
	storage.AccessToken = tokenData.AccessToken
	storage.RefreshToken = tokenData.RefreshToken
	storage.IDToken = tokenData.IDToken
	storage.Expire = tokenData.Expire
	storage.LastRefresh = time.Now().Format(time.RFC3339)
}

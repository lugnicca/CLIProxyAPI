// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT and OAuth token storage,
// serialization, and retrieval for maintaining authenticated sessions with
// the Grazie API and JetBrains Account OAuth.
package junie

import (
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

// JunieTokenData holds the raw OAuth token fields returned by the JetBrains token endpoint.
type JunieTokenData struct {
	// AccessToken is the OAuth2 access token.
	AccessToken string
	// RefreshToken is the OAuth2 refresh token.
	RefreshToken string
	// IDToken is the OpenID Connect ID token.
	IDToken string
	// Expire is the RFC3339 expiry timestamp of the access token.
	Expire string
}

// JunieAuthBundle aggregates authentication data after the OAuth PKCE flow completes.
// It wraps the raw token data together with the refresh timestamp.
type JunieAuthBundle struct {
	// TokenData contains the OAuth tokens from the JetBrains Account authentication flow.
	TokenData JunieTokenData
	// LastRefresh is the RFC3339 timestamp of when the tokens were last obtained.
	LastRefresh string
	// JWTToken is the legacy JWT token used in manual JWT mode (backward compatibility).
	JWTToken string
	// Label is a human-readable identifier for this token (e.g. account name or alias).
	Label string
}

// PKCECodes holds PKCE verification codes for the OAuth2 PKCE flow.
type PKCECodes struct {
	// CodeVerifier is the cryptographically random string used to correlate
	// the authorization request to the token request.
	CodeVerifier string `json:"code_verifier"`
	// CodeChallenge is the SHA256 hash of the code verifier, base64url-encoded.
	CodeChallenge string `json:"code_challenge"`
}

// JunieAuth handles authentication for the JetBrains Junie / Grazie API.
// It supports full OAuth2 PKCE flow via JetBrains Account, as well as
// legacy manual JWT token mode for backward compatibility.
type JunieAuth struct {
	httpClient *http.Client
}

// NewJunieAuth creates a new JunieAuth instance with an HTTP client configured
// using proxy settings from the application configuration.
//
// Parameters:
//   - cfg: The application configuration containing proxy settings
//
// Returns:
//   - *JunieAuth: A new JunieAuth instance
func NewJunieAuth(cfg *config.Config) *JunieAuth {
	client := util.SetProxy(&cfg.SDKConfig, &http.Client{})
	return &JunieAuth{
		httpClient: client,
	}
}

// CreateTokenStorage builds a JunieTokenStorage from a JunieAuthBundle.
// This is a package-level helper for backward compatibility and convenience.
// The resulting storage can be persisted to disk via SaveTokenToFile.
func CreateTokenStorage(bundle *JunieAuthBundle) *JunieTokenStorage {
	if bundle == nil {
		return &JunieTokenStorage{}
	}
	return &JunieTokenStorage{
		JWTToken:     bundle.JWTToken,
		Label:        bundle.Label,
		AccessToken:  bundle.TokenData.AccessToken,
		RefreshToken: bundle.TokenData.RefreshToken,
		IDToken:      bundle.TokenData.IDToken,
		Expire:       bundle.TokenData.Expire,
		LastRefresh:  bundle.LastRefresh,
	}
}

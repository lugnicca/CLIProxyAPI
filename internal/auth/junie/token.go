// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT and OAuth token storage,
// serialization, and retrieval for maintaining authenticated sessions with
// the Grazie API and JetBrains Account OAuth.
package junie

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	baseauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
)

// Compile-time check that JunieTokenStorage implements the TokenStorage interface.
var _ baseauth.TokenStorage = (*JunieTokenStorage)(nil)

// JunieTokenStorage stores token information for JetBrains Junie / Grazie API authentication.
// It supports both OAuth PKCE tokens (access_token, refresh_token, id_token) obtained via
// JetBrains Account OAuth flow, and legacy JWT tokens manually extracted from the JetBrains IDE.
type JunieTokenStorage struct {
	// AccessToken is the OAuth2 access token for authenticating requests to the Grazie API.
	AccessToken string `json:"access_token,omitempty"`
	// RefreshToken is the OAuth2 refresh token used to obtain new access tokens.
	RefreshToken string `json:"refresh_token,omitempty"`
	// IDToken is the OpenID Connect ID token containing user identity information.
	IDToken string `json:"id_token,omitempty"`
	// JWTToken is the legacy JWT token manually extracted from the JetBrains IDE.
	// Kept for backward compatibility with manual JWT mode.
	JWTToken string `json:"jwt_token,omitempty"`
	// Type indicates the authentication provider type, always "junie" for this storage.
	Type string `json:"type"`
	// Email is the JetBrains Account email address associated with this token.
	Email string `json:"email,omitempty"`
	// Label is a human-readable identifier for this token (e.g. account name or alias).
	Label string `json:"label,omitempty"`
	// Expire is the RFC3339 timestamp when the current access token expires.
	Expire string `json:"expired,omitempty"`
	// LastRefresh is the RFC3339 timestamp of when the tokens were last refreshed.
	LastRefresh string `json:"last_refresh,omitempty"`

	// Metadata holds arbitrary key-value pairs injected via hooks.
	// It is not exported to JSON directly to allow flattening during serialization.
	Metadata map[string]any `json:"-"`
}

// SetMetadata allows external callers to inject metadata into the storage before saving.
func (ts *JunieTokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile serializes the Junie token storage to a JSON file.
// This method creates the necessary directory structure and writes the token
// data in JSON format to the specified file path for persistent storage.
// It merges any injected metadata into the top-level JSON object.
//
// Parameters:
//   - authFilePath: The full path where the token file should be saved
//
// Returns:
//   - error: An error if the operation fails, nil otherwise
func (ts *JunieTokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "junie"
	if err := os.MkdirAll(filepath.Dir(authFilePath), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	f, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	// Merge metadata using helper
	data, errMerge := misc.MergeMetadata(ts, ts.Metadata)
	if errMerge != nil {
		return fmt.Errorf("failed to merge metadata: %w", errMerge)
	}

	if err = json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("failed to write token to file: %w", err)
	}
	return nil
}

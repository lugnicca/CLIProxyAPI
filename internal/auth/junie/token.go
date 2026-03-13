// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT token storage, serialization,
// and retrieval for maintaining authenticated sessions with the Grazie API.
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

// JunieTokenStorage stores JWT token information for JetBrains Junie / Grazie API authentication.
// Unlike OAuth-based providers, Junie tokens are JWT tokens manually extracted from
// the JetBrains IDE and provided by the user directly.
type JunieTokenStorage struct {
	// JWTToken is the JWT token used for authenticating requests to the Grazie API.
	JWTToken string `json:"jwt_token"`
	// Type indicates the authentication provider type, always "junie" for this storage.
	Type string `json:"type"`
	// Label is a human-readable identifier for this token (e.g. account name or alias).
	Label string `json:"label"`
	// LastRefresh is the timestamp of when this token was last stored or verified.
	LastRefresh string `json:"last_refresh"`

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

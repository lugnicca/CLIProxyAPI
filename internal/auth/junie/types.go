// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT token storage, serialization,
// and retrieval for maintaining authenticated sessions with the Grazie API.
package junie

// JunieAuthBundle holds the data provided by the user when registering a Junie account.
// Since Junie uses JWT tokens extracted directly from the JetBrains IDE (no OAuth flow),
// the bundle contains only the raw token and an optional label.
type JunieAuthBundle struct {
	// JWTToken is the JWT token extracted from the JetBrains IDE.
	JWTToken string
	// Label is a human-readable identifier for this token (e.g. account name or alias).
	Label string
}

// JunieAuth handles authentication for the JetBrains Junie / Grazie API.
// It is intentionally stateless: there is no OAuth flow, no PKCE, and no callback server.
// Users supply their JWT token directly, and this type provides helpers to package it
// into a persistent JunieTokenStorage.
type JunieAuth struct{}

// NewJunieAuth creates a new JunieAuth instance.
func NewJunieAuth() *JunieAuth {
	return &JunieAuth{}
}

// CreateTokenStorage builds a JunieTokenStorage from a JunieAuthBundle.
// The resulting storage can be persisted to disk via SaveTokenToFile.
func CreateTokenStorage(bundle *JunieAuthBundle) *JunieTokenStorage {
	return &JunieTokenStorage{
		JWTToken: bundle.JWTToken,
		Label:    bundle.Label,
	}
}

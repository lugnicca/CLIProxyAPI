// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT and OAuth token storage,
// serialization, and retrieval for maintaining authenticated sessions with
// the Grazie API and JetBrains Account OAuth.
package junie

import "errors"

// ErrInvalidJWT is returned when the provided JWT token is malformed or invalid.
var ErrInvalidJWT = errors.New("invalid JWT token")

// ErrJWTExpired is returned when the provided JWT token has expired.
var ErrJWTExpired = errors.New("JWT token expired")

// ErrCodeExchangeFailed is returned when the OAuth2 authorization code exchange fails.
var ErrCodeExchangeFailed = errors.New("OAuth2 code exchange failed")

// ErrServerStartFailed is returned when the local OAuth callback server fails to start.
var ErrServerStartFailed = errors.New("OAuth callback server failed to start")

// ErrCallbackTimeout is returned when the OAuth callback server times out waiting for a response.
var ErrCallbackTimeout = errors.New("timed out waiting for OAuth callback")

// ErrInvalidState is returned when the OAuth state parameter does not match.
var ErrInvalidState = errors.New("OAuth state parameter mismatch")

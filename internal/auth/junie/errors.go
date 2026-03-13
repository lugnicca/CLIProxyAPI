// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT token storage, serialization,
// and retrieval for maintaining authenticated sessions with the Grazie API.
package junie

import "errors"

// ErrInvalidJWT is returned when the provided JWT token is malformed or invalid.
var ErrInvalidJWT = errors.New("invalid JWT token")

// ErrJWTExpired is returned when the provided JWT token has expired.
var ErrJWTExpired = errors.New("JWT token expired")

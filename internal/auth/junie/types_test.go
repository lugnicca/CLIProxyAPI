package junie

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestNewJunieAuth(t *testing.T) {
	auth := NewJunieAuth(&config.Config{})
	if auth == nil {
		t.Fatal("NewJunieAuth() returned nil, expected non-nil *JunieAuth")
	}
}

func TestCreateTokenStorage(t *testing.T) {
	bundle := &JunieAuthBundle{
		JWTToken: "bundle-jwt-token",
		Label:    "test-label",
	}

	storage := CreateTokenStorage(bundle)
	if storage == nil {
		t.Fatal("CreateTokenStorage returned nil")
	}

	if storage.JWTToken != bundle.JWTToken {
		t.Errorf("expected JWTToken=%q, got %q", bundle.JWTToken, storage.JWTToken)
	}

	if storage.Label != bundle.Label {
		t.Errorf("expected Label=%q, got %q", bundle.Label, storage.Label)
	}
}

func TestCreateTokenStorageEmptyBundle(t *testing.T) {
	bundle := &JunieAuthBundle{}
	storage := CreateTokenStorage(bundle)
	if storage == nil {
		t.Fatal("CreateTokenStorage with empty bundle returned nil")
	}
	if storage.JWTToken != "" {
		t.Errorf("expected empty JWTToken, got %q", storage.JWTToken)
	}
	if storage.Label != "" {
		t.Errorf("expected empty Label, got %q", storage.Label)
	}
}

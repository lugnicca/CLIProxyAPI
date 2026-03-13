package junie

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	baseauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth"
)

func TestSaveTokenToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "junie_token.json")

	ts := &JunieTokenStorage{
		JWTToken: "test-jwt-token-123",
		Label:    "my-account",
	}

	if err := ts.SaveTokenToFile(path); err != nil {
		t.Fatalf("SaveTokenToFile returned unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written token file: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("token file is not valid JSON: %v", err)
	}

	if got, ok := out["jwt_token"]; !ok || got != "test-jwt-token-123" {
		t.Errorf("expected jwt_token=%q, got %v", "test-jwt-token-123", got)
	}

	if got, ok := out["type"]; !ok || got != "junie" {
		t.Errorf("expected type=%q, got %v", "junie", got)
	}

	if got, ok := out["label"]; !ok || got != "my-account" {
		t.Errorf("expected label=%q, got %v", "my-account", got)
	}
}

func TestSaveTokenToFileCreatesDir(t *testing.T) {
	base := t.TempDir()
	// Nested directories that do not yet exist.
	path := filepath.Join(base, "a", "b", "c", "token.json")

	ts := &JunieTokenStorage{
		JWTToken: "jwt-nested",
	}

	if err := ts.SaveTokenToFile(path); err != nil {
		t.Fatalf("SaveTokenToFile should create missing parent dirs, got error: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected token file to exist at %s", path)
	}
}

func TestSetMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token_meta.json")

	ts := &JunieTokenStorage{
		JWTToken: "jwt-with-meta",
	}

	meta := map[string]any{
		"extra_field": "extra_value",
		"count":       float64(42),
	}
	ts.SetMetadata(meta)

	if ts.Metadata == nil {
		t.Fatal("SetMetadata did not set Metadata field")
	}
	if ts.Metadata["extra_field"] != "extra_value" {
		t.Errorf("unexpected metadata value: %v", ts.Metadata["extra_field"])
	}

	// Persist and verify metadata is merged into the file.
	if err := ts.SaveTokenToFile(path); err != nil {
		t.Fatalf("SaveTokenToFile with metadata failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read token file: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("token file is not valid JSON: %v", err)
	}

	if got, ok := out["extra_field"]; !ok || got != "extra_value" {
		t.Errorf("expected extra_field to be merged into file, got %v", got)
	}
}

func TestTokenStorageInterface(t *testing.T) {
	// Compile-time assertion: JunieTokenStorage must satisfy baseauth.TokenStorage.
	// This also works as a runtime check.
	var _ baseauth.TokenStorage = (*JunieTokenStorage)(nil)

	ts := &JunieTokenStorage{JWTToken: "interface-check"}
	dir := t.TempDir()
	path := filepath.Join(dir, "interface_token.json")

	var storage baseauth.TokenStorage = ts
	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("calling SaveTokenToFile through interface failed: %v", err)
	}
}

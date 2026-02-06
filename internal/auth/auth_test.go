package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetAPIKeyFromEnv(t *testing.T) {
	const testKey = "test-api-key-12345"

	originalKey := os.Getenv("GEMINI_API_KEY")
	defer os.Setenv("GEMINI_API_KEY", originalKey)

	os.Setenv("GEMINI_API_KEY", testKey)

	key, err := GetAPIKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if key != testKey {
		t.Errorf("expected key %q, got %q", testKey, key)
	}
}

func TestGetAPIKeyNoSource(t *testing.T) {
	originalKey := os.Getenv("GEMINI_API_KEY")
	defer os.Setenv("GEMINI_API_KEY", originalKey)

	os.Unsetenv("GEMINI_API_KEY")

	// Create a temporary home directory without credentials
	tmpHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tmpHome)

	_, err := GetAPIKey()
	if err == nil {
		t.Error("expected error when no API key source available")
	}
}

func TestGetCredentialPath(t *testing.T) {
	path, err := getCredentialPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".gemini-media-cli", "credentials.gpg")

	if path != expected {
		t.Errorf("expected path %q, got %q", expected, path)
	}
}

func TestGetFromGPGFileNotFound(t *testing.T) {
	// Create a temporary home directory without credentials
	tmpHome := t.TempDir()
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tmpHome)

	_, err := getFromGPG()
	if err == nil {
		t.Error("expected error when credentials file does not exist")
	}
}


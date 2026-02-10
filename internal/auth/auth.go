package auth

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

const (
	credentialDir  = ".gemini-media-cli"
	credentialFile = "credentials.gpg"
)

// GetAPIKey retrieves the Gemini API key from available sources.
// Priority order:
//  1. GEMINI_API_KEY environment variable
//  2. GPG-encrypted file at ~/.gemini-media-cli/credentials.gpg
func GetAPIKey() (string, error) {
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		log.Debug().Msg("Using API key from environment variable")
		return key, nil
	}

	key, err := getFromGPG()
	if err == nil && key != "" {
		log.Debug().Msg("Using API key from GPG encrypted file")
		return key, nil
	}

	log.Error().Err(err).Msg("Failed to retrieve API key")
	return "", fmt.Errorf("API key not found. Set GEMINI_API_KEY or run scripts/setup-gpg-credentials.sh")
}

// getFromGPG decrypts the API key from the GPG-encrypted credentials file.
func getFromGPG() (string, error) {
	credPath, err := getCredentialPath()
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(credPath); os.IsNotExist(err) {
		return "", fmt.Errorf("GPG credentials file not found at %s", credPath)
	}

	log.Debug().Str("file", credPath).Msg("Decrypting GPG credentials")

	// Build GPG command with optional passphrase file for non-interactive use
	args := []string{"--decrypt", "--quiet"}

	passphrasePath, err := getPassphrasePath()
	if err == nil {
		fi, statErr := os.Stat(passphrasePath)
		if statErr == nil {
			// Verify file permissions — passphrase file must be owner-only (DDR-028 Problem 14)
			mode := fi.Mode().Perm()
			if mode&0077 != 0 {
				log.Warn().
					Str("passphrase_file", passphrasePath).
					Str("permissions", fmt.Sprintf("%04o", mode)).
					Msg("Passphrase file has insecure permissions (should be 0600); skipping")
			} else {
				log.Debug().Str("passphrase_file", passphrasePath).Msg("Using passphrase file for GPG decryption")
				args = append(args, "--pinentry-mode", "loopback", "--passphrase-file", passphrasePath)
			}
		}
	}

	args = append(args, credPath)
	cmd := exec.Command("gpg", args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("GPG decryption failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("GPG decryption failed: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// getCredentialPath returns the full path to the credentials file.
func getCredentialPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(home, credentialDir, credentialFile), nil
}

// getPassphrasePath returns the path to the GPG passphrase file in the project directory.
// This allows non-interactive GPG decryption when running in automated environments.
func getPassphrasePath() (string, error) {
	// Get the executable's directory to find .gpg-passphrase relative to project
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// Check in the same directory as the executable
	exeDir := filepath.Dir(exe)
	passphrasePath := filepath.Join(exeDir, ".gpg-passphrase")
	if _, err := os.Stat(passphrasePath); err == nil {
		return passphrasePath, nil
	}

	// Also check current working directory (for development)
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	passphrasePath = filepath.Join(cwd, ".gpg-passphrase")
	return passphrasePath, nil
}

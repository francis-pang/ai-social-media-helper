# Authentication & Key Storage Design Document

## Overview

This document describes how to securely store and manage the Gemini API key for the Gemini Media Analysis CLI. The goal is to keep credentials **outside the code repository** while enabling seamless usage across multiple machines.

---

## Key Storage Options

### Option 1: System Keychain (Recommended)

Store the API key in your operating system's secure credential storage.

#### macOS - Keychain Access

**Store the key:**
```bash
security add-generic-password \
    -a "$USER" \
    -s "gemini-media-cli" \
    -w "your-api-key-here"
```

**Retrieve the key:**
```bash
security find-generic-password \
    -a "$USER" \
    -s "gemini-media-cli" \
    -w
```

**Delete the key:**
```bash
security delete-generic-password \
    -a "$USER" \
    -s "gemini-media-cli"
```

#### Linux - libsecret / GNOME Keyring

**Store the key:**
```bash
secret-tool store --label="Gemini Media CLI" \
    service gemini-media-cli \
    username "$USER" <<< "your-api-key-here"
```

**Retrieve the key:**
```bash
secret-tool lookup service gemini-media-cli username "$USER"
```

#### Windows - Credential Manager

**Store the key (PowerShell):**
```powershell
cmdkey /generic:gemini-media-cli /user:$env:USERNAME /pass:"your-api-key-here"
```

**Retrieve the key:**
Use Windows Credential Manager GUI or:
```powershell
$cred = Get-StoredCredential -Target "gemini-media-cli"
$cred.GetNetworkCredential().Password
```

---

### Option 2: Encrypted File (Cross-Platform)

Store the API key in an encrypted file that you can sync across machines.

#### Using GPG Encryption

**Initial setup (one time):**
```bash
# Generate a GPG key if you don't have one
gpg --full-generate-key

# Create encrypted credentials file
echo "your-api-key-here" | gpg --encrypt --recipient your-email@example.com \
    -o ~/.gemini-media-cli/credentials.gpg
```

**Retrieve the key:**
```bash
gpg --decrypt ~/.gemini-media-cli/credentials.gpg 2>/dev/null
```

**Sync across machines:**
- Sync `~/.gemini-media-cli/credentials.gpg` via cloud storage
- Export your GPG key to other machines: `gpg --export-secret-keys > private.key`
- Import on new machine: `gpg --import private.key`

---

### Option 3: Environment Variable with Shell Profile

Store the key in your shell profile, which is typically not version-controlled.

#### Zsh / Bash

Add to `~/.zshrc` or `~/.bashrc`:
```bash
export GEMINI_API_KEY="your-api-key-here"
```

**Better approach - source from separate file:**
```bash
# In ~/.zshrc or ~/.bashrc
if [[ -f ~/.secrets/gemini ]]; then
    source ~/.secrets/gemini
fi
```

```bash
# In ~/.secrets/gemini (create this file)
export GEMINI_API_KEY="your-api-key-here"
```

**Sync across machines:**
- Do NOT commit `~/.secrets/` to any repository
- Manually copy or use encrypted sync (see GPG method above)

---

### Option 4: 1Password / Bitwarden CLI (Team-Friendly)

Use a password manager CLI for team environments.

#### 1Password CLI

**Setup:**
```bash
# Install 1Password CLI
brew install 1password-cli

# Sign in
op signin
```

**Store the key:**
Create an item in 1Password with:
- Title: `Gemini Media CLI`
- Field: `api_key` containing your API key

**Retrieve the key:**
```bash
export GEMINI_API_KEY=$(op read "op://Personal/Gemini Media CLI/api_key")
```

#### Bitwarden CLI

**Setup:**
```bash
# Install Bitwarden CLI
brew install bitwarden-cli

# Log in and unlock
bw login
export BW_SESSION=$(bw unlock --raw)
```

**Store the key:**
```bash
bw create item '{"type":1,"name":"Gemini Media CLI","login":{"password":"your-api-key-here"}}'
```

**Retrieve the key:**
```bash
export GEMINI_API_KEY=$(bw get password "Gemini Media CLI")
```

---

## Application Integration

### Keychain Integration in Go

The CLI will attempt to retrieve the API key in this order:

1. `--api-key` command-line flag
2. `GEMINI_API_KEY` environment variable
3. System keychain (macOS Keychain / Linux libsecret / Windows Credential Manager)
4. GPG-encrypted file at `~/.gemini-media-cli/credentials.gpg`

```go
package auth

import (
    "fmt"
    "os"
    "os/exec"
    "runtime"
    "strings"
)

func GetAPIKey() (string, error) {
    // 1. Check environment variable first
    if key := os.Getenv("GEMINI_API_KEY"); key != "" {
        return key, nil
    }
    
    // 2. Try system keychain
    key, err := getFromKeychain()
    if err == nil && key != "" {
        return key, nil
    }
    
    // 3. Try GPG encrypted file
    key, err = getFromGPG()
    if err == nil && key != "" {
        return key, nil
    }
    
    return "", fmt.Errorf("API key not found. See 'gemini-cli auth --help' for setup instructions")
}

func getFromKeychain() (string, error) {
    switch runtime.GOOS {
    case "darwin":
        return getFromMacOSKeychain()
    case "linux":
        return getFromLinuxKeyring()
    case "windows":
        return getFromWindowsCredential()
    default:
        return "", fmt.Errorf("unsupported OS for keychain: %s", runtime.GOOS)
    }
}

func getFromMacOSKeychain() (string, error) {
    cmd := exec.Command("security", "find-generic-password",
        "-a", os.Getenv("USER"),
        "-s", "gemini-media-cli",
        "-w")
    
    output, err := cmd.Output()
    if err != nil {
        return "", err
    }
    
    return strings.TrimSpace(string(output)), nil
}

func getFromLinuxKeyring() (string, error) {
    cmd := exec.Command("secret-tool", "lookup",
        "service", "gemini-media-cli",
        "username", os.Getenv("USER"))
    
    output, err := cmd.Output()
    if err != nil {
        return "", err
    }
    
    return strings.TrimSpace(string(output)), nil
}

func getFromWindowsCredential() (string, error) {
    // Use go-keyring library for Windows
    // or call cmdkey via PowerShell
    return "", fmt.Errorf("Windows credential manager not implemented")
}

func getFromGPG() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil {
        return "", err
    }
    
    credFile := home + "/.gemini-media-cli/credentials.gpg"
    if _, err := os.Stat(credFile); os.IsNotExist(err) {
        return "", fmt.Errorf("GPG credentials file not found")
    }
    
    cmd := exec.Command("gpg", "--decrypt", "--quiet", credFile)
    output, err := cmd.Output()
    if err != nil {
        return "", err
    }
    
    return strings.TrimSpace(string(output)), nil
}
```

---

## CLI Commands

### Authentication Setup

```bash
# Interactive setup wizard
gemini-cli auth setup

# Store key in system keychain
gemini-cli auth store --keychain

# Store key in GPG-encrypted file  
gemini-cli auth store --gpg

# Verify authentication works
gemini-cli auth verify

# Show current auth method (without revealing key)
gemini-cli auth status

# Remove stored credentials
gemini-cli auth remove
```

### Example: `auth setup` Flow

```
$ gemini-cli auth setup

Welcome to Gemini Media CLI Authentication Setup!

Where would you like to store your API key?

  1. System Keychain (recommended)
     Secure, native OS credential storage
     
  2. GPG Encrypted File
     Portable, can be synced across machines
     
  3. Environment Variable
     Set GEMINI_API_KEY in your shell profile

Choose [1-3]: 1

Enter your Gemini API Key: ****************************************

✓ API key stored in macOS Keychain
✓ Verification successful - connected to Gemini API

You're all set! Run 'gemini-cli upload <file>' to get started.
```

---

## Obtaining a Gemini API Key

### Step-by-Step Instructions

1. **Go to Google AI Studio**
   - Visit: https://aistudio.google.com/

2. **Sign in with Google**
   - Use your Google account to sign in

3. **Navigate to API Keys**
   - Click on "Get API key" in the left sidebar
   - Or visit: https://aistudio.google.com/app/apikey

4. **Create a new API key**
   - Click "Create API key"
   - Choose or create a Google Cloud project
   - Copy the generated key

5. **Store the key securely**
   - Use one of the methods described above
   - **Never commit the key to version control**

### API Key Format

Gemini API keys typically look like:
```
AIzaSy...39 characters total
```

---

## Cross-Machine Setup

### Recommended Workflow

1. **Primary machine setup:**
   ```bash
   # Store in keychain
   gemini-cli auth store --keychain
   
   # Also create GPG backup for syncing
   gemini-cli auth export --gpg > ~/.gemini-media-cli/credentials.gpg
   ```

2. **Sync the GPG file:**
   - Use iCloud Drive, Dropbox, or similar
   - Or manually copy via SSH/USB

3. **New machine setup:**
   ```bash
   # Import your GPG key first
   gpg --import ~/path/to/private.key
   
   # Clone your repo
   git clone <your-repo>
   
   # Run auth setup (will find GPG file)
   gemini-cli auth setup
   
   # Optionally migrate to local keychain
   gemini-cli auth store --keychain
   ```

### Quick One-Liner for New Machines

If you have GPG set up and credentials synced:
```bash
export GEMINI_API_KEY=$(gpg -d ~/.gemini-media-cli/credentials.gpg 2>/dev/null)
```

Add to `~/.zshrc` for persistence.

---

## Security Best Practices

### DO ✅

- Use system keychain when possible (most secure)
- Use GPG encryption for portable storage
- Verify file permissions: `chmod 600 ~/.gemini-media-cli/credentials.gpg`
- Use different API keys for different purposes if needed
- Regenerate keys if you suspect compromise

### DON'T ❌

- Never commit API keys to Git repositories
- Never paste keys in plain text files in your repo
- Never share keys via unencrypted channels (email, Slack)
- Never log API keys (app handles this automatically)
- Never hardcode keys in source code

### Git Safety

Add to your global `.gitignore`:
```gitignore
# Gemini CLI credentials
.gemini-media-cli/credentials*
.gemini-media-cli/*.gpg

# Generic secrets
.secrets/
*.key
*.pem
```

Add a pre-commit hook to prevent accidental commits:
```bash
#!/bin/bash
# .git/hooks/pre-commit

# Check for potential API keys
if git diff --cached | grep -qE 'AIzaSy[a-zA-Z0-9_-]{33}'; then
    echo "ERROR: Possible Gemini API key detected in commit!"
    echo "Remove the key and use secure storage instead."
    exit 1
fi
```

---

## Troubleshooting

### "API key not found"

1. Check environment variable:
   ```bash
   echo $GEMINI_API_KEY
   ```

2. Check keychain:
   ```bash
   # macOS
   security find-generic-password -a "$USER" -s "gemini-media-cli" -w
   ```

3. Check GPG file:
   ```bash
   ls -la ~/.gemini-media-cli/credentials.gpg
   gpg -d ~/.gemini-media-cli/credentials.gpg
   ```

4. Run auth status:
   ```bash
   gemini-cli auth status
   ```

### "Invalid API key"

1. Verify the key at https://aistudio.google.com/app/apikey
2. Check for trailing whitespace/newlines
3. Ensure the key hasn't been revoked
4. Try regenerating a new key

### "Keychain access denied"

- macOS: Allow terminal access in System Preferences → Security & Privacy → Privacy → Keychain Access
- Linux: Ensure `gnome-keyring-daemon` is running
- Try running `gemini-cli auth store --keychain` again

---

## Summary

| Method | Security | Portability | Ease of Use | Best For |
|--------|----------|-------------|-------------|----------|
| System Keychain | ⭐⭐⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐⭐ | Single machine, daily use |
| GPG Encrypted | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | Multiple machines |
| Env Variable | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | CI/CD, scripts |
| Password Manager | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | Team environments |

**Recommendation**: Use **System Keychain** on your primary machine with **GPG backup** for syncing to other machines.

---

**Last Updated**: 2025-12-30  
**Version**: 1.0.0

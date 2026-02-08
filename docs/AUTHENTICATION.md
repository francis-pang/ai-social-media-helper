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

### API Key Retrieval Priority

The CLI retrieves the API key in this order:

1. `GEMINI_API_KEY` environment variable (highest priority)
2. GPG-encrypted file at `~/.gemini-media-cli/credentials.gpg`

If neither source provides a valid API key, the CLI exits with an error message instructing the user to set up credentials.

### API Key Validation

After retrieving the API key, the CLI validates it by making a lightweight API call to the Gemini API. This ensures the key is valid before proceeding with any operations.

**Validation Model**: `gemini-3-flash-preview` (free tier compatible)

See [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing) for current model pricing and free tier limits.

The validation makes a minimal request ("hi") to verify:
- The API key is correctly formatted
- The key has not been revoked
- Network connectivity to Gemini API is available
- The account has available quota

---

## API Key Validation Error Handling

The CLI categorizes API key validation errors into five distinct types, each with specific user guidance:

### Error Types

| Error Type | HTTP Codes | Description | User Message |
|------------|------------|-------------|--------------|
| **No Key** | N/A | No API key found in any source | "Set GEMINI_API_KEY or run scripts/setup-gpg-credentials.sh" |
| **Invalid Key** | 400, 401, 403 | Key is malformed, expired, or lacks permissions | "Invalid API key. Please check your API key and try again" |
| **Network Error** | 500, 502, 503, 504, connection errors | Connectivity or server issues | "Network error. Please check your internet connection" |
| **Quota Exceeded** | 429 | Rate limit or quota exhausted | "API quota exceeded. Please try again later or check your usage limits" |
| **Unknown** | Other | Unclassified errors | "API key validation failed" |

### Error Detection

The validation logic examines:

1. **Google API HTTP status codes** - Direct classification based on HTTP response codes
2. **Error message patterns** - Keyword matching for phrases like "API key not valid", "quota", "connection", etc.
3. **Network-level errors** - Detection of timeout, dial, and unreachable errors

### Logging Behavior

- **Debug level**: Logs the credential source being used and validation attempt
- **Error level**: Logs detailed error information with HTTP codes when available
- **Fatal level**: Logs user-friendly message and exits on validation failure

---

## Design Decisions

### Credential Source Priority

**Decision**: Environment variable takes priority over GPG-encrypted file.

**Rationale**:
- **CI/CD compatibility**: Environment variables are the standard way to inject secrets in CI pipelines
- **Override capability**: Users can temporarily override stored credentials for testing
- **Simplicity**: Most common use case (env var) is checked first, minimizing latency
- **Security**: GPG file requires passphrase entry, which may fail in non-interactive contexts

**Priority Order**:
1. `GEMINI_API_KEY` environment variable
2. GPG-encrypted file at `~/.gemini-media-cli/credentials.gpg`

### GPG Over System Keychain

**Decision**: Current implementation uses GPG encryption rather than system keychain for portable credential storage.

**Rationale**:
- **Cross-platform**: GPG works identically on macOS, Linux, and Windows
- **Portable**: Encrypted file can be synced via cloud storage
- **No dependencies**: Uses system `gpg` binary, no additional libraries needed
- **Developer-friendly**: GPG is commonly installed on developer machines

**Trade-offs**:
- Requires GPG key setup (one-time)
- Passphrase entry required (can be cached by gpg-agent)
- System keychain integration deferred to future iteration

### Non-Interactive GPG Decryption

**Decision**: Support passphrase file (`.gpg-passphrase`) for automated/non-interactive environments.

**Rationale**:
- **CI/CD compatibility**: Automated pipelines cannot enter passphrases interactively
- **Development convenience**: Avoids repeated passphrase entry during development
- **Security via gitignore**: Passphrase file is gitignored, never committed to version control
- **Fallback behavior**: If passphrase file is missing, falls back to interactive GPG agent

**Implementation**:
- Passphrase file location: `.gpg-passphrase` in project root or executable directory
- File permissions: Should be `600` (owner read/write only)
- GPG flags used: `--pinentry-mode loopback --passphrase-file`
- Priority: Current working directory checked first, then executable directory

**Security Considerations**:
- `.gpg-passphrase` is added to `.gitignore` to prevent accidental commits
- Users accept risk of local file storage for convenience
- Alternative: Use GPG agent with cached passphrase for better security

### Validation Before Operations

**Decision**: Validate API key on startup, before any other operations.

**Rationale**:
- **Fail fast principle**: Users discover auth issues immediately, not after waiting for file uploads
- **Clear feedback**: Specific error messages guide users to resolution
- **Resource efficiency**: No wasted API calls with invalid credentials
- **Better UX**: Single validation request vs. cryptic errors during operations

**Validation Approach**:
- Minimal request ("hi") to `gemini-3-flash-preview`
- Validates: key format, key validity, network connectivity, quota availability
- Completes in ~1-2 seconds on fast connections

### Typed Error Handling

**Decision**: Use explicit `ValidationErrorType` enum instead of string-based errors.

**Rationale**:
- **Compile-time safety**: Switch statements ensure all error types are handled
- **Consistent messaging**: Each type maps to a specific, tested user message
- **Actionable guidance**: Error types inform specific resolution steps
- **Extensibility**: New error types integrate without breaking existing handlers

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

## Cloud Authentication (DDR-028)

In cloud mode (deployed to AWS), the application uses **Amazon Cognito User Pool** for user authentication instead of API keys.

### Architecture

```
Browser ──► CloudFront ──► API Gateway ──► Lambda
                              │
                         JWT Authorizer
                         (Cognito User Pool)
```

- The Cognito User Pool has **self-signup disabled** — users are provisioned by the admin
- API Gateway validates the JWT token before forwarding requests to Lambda
- The frontend SPA uses `amazon-cognito-identity-js` for authentication
- JWT tokens are stored in browser memory and refreshed automatically
- Health endpoint (`/api/health`) is unauthenticated for monitoring

### User Provisioning

Since self-signup is disabled, create users via AWS CLI:

```bash
# Create the user (sends a temporary password via email)
aws cognito-idp admin-create-user \
  --user-pool-id <user-pool-id> \
  --username <email> \
  --user-attributes Name=email,Value=<email> Name=email_verified,Value=true

# Set a permanent password (skip temporary password flow)
aws cognito-idp admin-set-user-password \
  --user-pool-id <user-pool-id> \
  --username <email> \
  --password '<password>' \
  --permanent
```

### Frontend Configuration

The frontend needs two environment variables at build time:

```bash
VITE_CLOUD_MODE=1
VITE_COGNITO_USER_POOL_ID=us-east-1_xxxxxxxxx
VITE_COGNITO_CLIENT_ID=xxxxxxxxxxxxxxxxxxxxxxxxxx
```

These are output by the CDK stack (`UserPoolId` and `UserPoolClientId`).

### Password Policy

- Minimum 12 characters
- Requires uppercase, lowercase, digits, and symbols
- Token validity: 1 hour (ID/access), 7 days (refresh)

See [DDR-028](./design-decisions/DDR-028-security-hardening.md) for the full security hardening decision record.

---

**Last Updated**: 2026-02-07
**Version**: 1.2.0

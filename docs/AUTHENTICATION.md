# Authentication & Key Storage

## Overview

The application uses different authentication mechanisms depending on the deployment mode:

- **Local mode** — Gemini API key via environment variable or GPG-encrypted file
- **Cloud mode** — Amazon Cognito User Pool for user authentication; Gemini API key loaded from SSM Parameter Store at Lambda cold start

## Credential Resolution (Local Mode)

```mermaid
flowchart TD
    Start["Application starts"]
    EnvVar{"GEMINI_API_KEY\nenv var set?"}
    GPGFile{"GPG file exists?\n~/.gemini-media-cli/credentials.gpg"}
    Passphrase{"Passphrase file?\n.gpg-passphrase"}
    Decrypt["Decrypt with\nGPG agent or\npassphrase file"]
    Validate["Validate key\n(minimal Gemini API call)"]
    Success["Key valid — proceed"]
    Fail["Exit with typed error"]

    Start --> EnvVar
    EnvVar -->|Yes| Validate
    EnvVar -->|No| GPGFile
    GPGFile -->|Yes| Passphrase
    GPGFile -->|No| Fail
    Passphrase -->|Yes| Decrypt
    Passphrase -->|No| Decrypt
    Decrypt --> Validate
    Validate -->|Valid| Success
    Validate -->|Invalid| Fail
```

**Priority order:**
1. `GEMINI_API_KEY` environment variable (highest — CI/CD compatible)
2. GPG-encrypted file at `~/.gemini-media-cli/credentials.gpg`

**Validation:** A minimal request ("hi") is sent to `gemini-3-flash-preview` at startup to verify the key is valid, not revoked, and has available quota. See [DDR-004](./design-decisions/DDR-004-startup-api-validation.md).

## GPG Credential Storage

Store and retrieve your API key using GPG encryption. See [DDR-003](./design-decisions/DDR-003-gpg-credential-storage.md).

**Setup:**
```bash
# Encrypt your API key (one-time)
echo "your-api-key-here" | gpg --encrypt --recipient your-email@example.com \
    -o ~/.gemini-media-cli/credentials.gpg

# Verify it works
gpg --decrypt ~/.gemini-media-cli/credentials.gpg 2>/dev/null
```

**Non-interactive decryption:** For CI/CD or development convenience, place your GPG passphrase in a `.gpg-passphrase` file in the project root (gitignored). The app uses `--pinentry-mode loopback --passphrase-file` for automatic decryption. See [DDR-003](./design-decisions/DDR-003-gpg-credential-storage.md).

**Cross-machine sync:** Copy `~/.gemini-media-cli/credentials.gpg` and import your GPG key (`gpg --import private.key`) on the new machine.

## Validation Error Handling

| Error Type | HTTP Codes | User Guidance |
|------------|------------|---------------|
| No Key | N/A | Set `GEMINI_API_KEY` or run `scripts/setup-gpg-credentials.sh` |
| Invalid Key | 400, 401, 403 | Check key at [Google AI Studio](https://aistudio.google.com/app/apikey) |
| Network Error | 500, 502, 503, 504 | Check internet connection |
| Quota Exceeded | 429 | Wait for reset or check usage limits |

See [DDR-005](./design-decisions/DDR-005-typed-validation-errors.md) for typed error handling design.

## Cloud Authentication (Cognito)

In cloud mode, user authentication uses Amazon Cognito User Pool with JWT tokens. See [DDR-028](./design-decisions/DDR-028-security-hardening.md).

```mermaid
sequenceDiagram
    participant User
    participant SPA as Preact SPA
    participant Cognito
    participant APIGW as API Gateway
    participant Lambda

    User->>SPA: Enter email + password
    SPA->>Cognito: Authenticate
    Cognito-->>SPA: JWT tokens (ID, access, refresh)
    SPA->>APIGW: API request + Authorization header
    APIGW->>Cognito: Validate JWT
    Cognito-->>APIGW: Token valid
    APIGW->>Lambda: Forward request
    Lambda-->>SPA: Response
```

**Key points:**
- Self-signup is disabled — users are provisioned by admin via AWS CLI
- API Gateway JWT authorizer validates tokens before forwarding to Lambda
- Frontend uses `amazon-cognito-identity-js` for authentication
- Tokens stored in browser memory, automatically refreshed
- Health endpoint (`/api/health`) is unauthenticated

**User provisioning:**
```bash
aws cognito-idp admin-create-user \
  --user-pool-id <pool-id> --username <email> \
  --user-attributes Name=email,Value=<email> Name=email_verified,Value=true

aws cognito-idp admin-set-user-password \
  --user-pool-id <pool-id> --username <email> \
  --password '<password>' --permanent
```

**Password policy:** Minimum 12 characters, requires uppercase + lowercase + digits + symbols. Token validity: 1 hour (ID/access), 7 days (refresh).

## Security Best Practices

- Never commit API keys to Git — `.gpg-passphrase` and credential files are gitignored
- Use environment variables for CI/CD pipelines
- Regenerate keys immediately if compromise is suspected
- File permissions: `chmod 600 ~/.gemini-media-cli/credentials.gpg`

## Related DDRs

- [DDR-003](./design-decisions/DDR-003-gpg-credential-storage.md) — GPG credential storage
- [DDR-004](./design-decisions/DDR-004-startup-api-validation.md) — Startup API key validation
- [DDR-005](./design-decisions/DDR-005-typed-validation-errors.md) — Typed validation errors
- [DDR-025](./design-decisions/DDR-025-ssm-parameter-store-secrets.md) — SSM Parameter Store for runtime secrets
- [DDR-028](./design-decisions/DDR-028-security-hardening.md) — Security hardening

---

**Last Updated**: 2026-02-09

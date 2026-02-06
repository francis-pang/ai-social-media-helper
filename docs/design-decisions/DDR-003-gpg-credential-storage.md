# DDR-003: GPG Credential Storage

**Date**: 2025-12-30  
**Status**: Accepted  
**Iteration**: 3-4

## Context

The CLI requires a Gemini API key. We needed a secure way to store this key that works for both interactive development and automated environments (CI/CD).

## Decision

Use GPG encryption for API key storage rather than plaintext or third-party secrets managers.

Priority order for API key retrieval:
1. `GEMINI_API_KEY` environment variable
2. GPG-encrypted file at `~/.gemini-media-cli/credentials.gpg`

## Rationale

- **Security**: AES-256 encryption protects keys at rest
- **Developer familiarity**: GPG is standard tooling for developers
- **Portability**: Encrypted files can be synced across machines (Dropbox, etc.)
- **No dependencies**: Uses system GPG binary, no additional packages
- **Flexibility**: Environment variable override for CI/CD

## Non-Interactive Support

For automated environments, support a passphrase file:
- Location: `.gpg-passphrase` in project root or executable directory
- GPG flags: `--pinentry-mode loopback --passphrase-file`
- Fallback: Interactive GPG agent if passphrase file is missing
- Security: Passphrase file is gitignored

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Plaintext file | Insecure; accidental commits |
| macOS Keychain | Not cross-platform |
| AWS Secrets Manager | Overkill for CLI; requires AWS setup |
| HashiCorp Vault | Too complex for single-user CLI |
| `.env` file | No encryption; easy to commit accidentally |

## Consequences

- **Positive**: Secure key storage with industry-standard encryption
- **Positive**: Works across macOS, Linux, Windows
- **Trade-off**: Requires GPG key setup (documented in setup script)
- **Trade-off**: Passphrase entry needed (gpg-agent caches for session)

## Related Documents

- [authentication.md](../authentication.md) - Full authentication design


#!/bin/bash
# setup-gpg-credentials.sh
# Encrypts and stores Gemini API key using GPG
# Supports both RSA and Ed25519 keys

set -e

CRED_DIR="$HOME/.gemini-media-cli"
CRED_FILE="$CRED_DIR/credentials.gpg"

echo "=== Gemini Media CLI - GPG Credential Setup ==="
echo ""

# Check if GPG is installed
if ! command -v gpg &> /dev/null; then
    echo "Error: GPG is not installed."
    echo "Install with: brew install gnupg"
    exit 1
fi

# Check GPG version (need 2.1+ for Ed25519 support)
GPG_VERSION=$(gpg --version | head -1 | grep -oE '[0-9]+\.[0-9]+' | head -1)
echo "GPG version: $GPG_VERSION"

# List available GPG keys
echo ""
echo "Available GPG keys:"
echo "-------------------"
gpg --list-keys --keyid-format SHORT 2>/dev/null | grep -E "^(pub|uid)" || true
echo ""

# Get list of key emails
EMAILS=$(gpg --list-keys --with-colons 2>/dev/null | grep "^uid" | cut -d: -f10 | grep -oE '<[^>]+>' | tr -d '<>' | sort -u)

if [ -z "$EMAILS" ]; then
    echo "No GPG keys found!"
    echo ""
    echo "To generate a new Ed25519 key (recommended), run:"
    echo "  gpg --quick-generate-key \"Your Name <your.email@example.com>\" ed25519 cert,sign+encr 2y"
    echo ""
    echo "Or for an interactive setup:"
    echo "  gpg --full-generate-key --expert"
    echo "  (Select option 9 for ECC, then Curve 25519)"
    exit 1
fi

# Count available keys
KEY_COUNT=$(echo "$EMAILS" | wc -l | tr -d ' ')

if [ "$KEY_COUNT" -eq 1 ]; then
    GPG_RECIPIENT="$EMAILS"
    echo "Using GPG key: $GPG_RECIPIENT"
else
    echo "Multiple GPG keys found. Please select one:"
    echo ""
    
    # Create array of emails
    i=1
    while IFS= read -r email; do
        echo "  $i) $email"
        i=$((i + 1))
    done <<< "$EMAILS"
    
    echo ""
    printf "Enter selection (1-%s): " "$KEY_COUNT"
    read -r SELECTION
    
    # Validate selection
    if ! [[ "$SELECTION" =~ ^[0-9]+$ ]] || [ "$SELECTION" -lt 1 ] || [ "$SELECTION" -gt "$KEY_COUNT" ]; then
        echo "Invalid selection"
        exit 1
    fi
    
    GPG_RECIPIENT=$(echo "$EMAILS" | sed -n "${SELECTION}p")
    echo ""
    echo "Selected: $GPG_RECIPIENT"
fi

# Create credential directory
mkdir -p "$CRED_DIR"
chmod 700 "$CRED_DIR"

# Check if credentials already exist
if [ -f "$CRED_FILE" ]; then
    echo ""
    echo "Warning: Existing credentials found at $CRED_FILE"
    printf "Overwrite? (y/N): "
    read -r CONFIRM
    if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
        echo "Aborted."
        exit 0
    fi
fi

# Prompt for API key
echo ""
printf "Enter your Gemini API Key: "
stty -echo
read -r API_KEY
stty echo
echo ""

if [ -z "$API_KEY" ]; then
    echo "Error: API key cannot be empty"
    exit 1
fi

# Validate API key format (basic check - Gemini keys start with "AI")
if [[ ! "$API_KEY" =~ ^AI ]]; then
    echo ""
    echo "Warning: API key doesn't appear to start with 'AI'."
    echo "Gemini API keys typically start with 'AI' (e.g., AIza...)."
    printf "Continue anyway? (y/N): "
    read -r CONFIRM
    if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
        echo "Aborted."
        exit 1
    fi
fi

# Encrypt and store
echo ""
echo "Encrypting API key with GPG..."
echo "$API_KEY" | gpg --encrypt --recipient "$GPG_RECIPIENT" --armor -o "$CRED_FILE" 2>/dev/null

if [ $? -eq 0 ]; then
    chmod 600 "$CRED_FILE"
    echo ""
    echo "✓ API key encrypted and stored at: $CRED_FILE"
    echo ""
    echo "To verify, run:"
    echo "  gpg --decrypt $CRED_FILE"
    echo ""
    echo "The CLI will automatically use this key when GEMINI_API_KEY is not set."
else
    echo "Error: Encryption failed"
    exit 1
fi


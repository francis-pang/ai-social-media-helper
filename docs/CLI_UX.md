# CLI UX Design Document

## Overview

This document outlines CLI user experience design decisions for the Gemini Media Analysis CLI. Review these options to make design decisions for your implementation.

---

## Command Structure Options

### Option A: Flat Commands (Recommended for simple CLIs)

```bash
gemini-cli upload photo.jpg
gemini-cli ask "What's in this image?"
gemini-cli list-sessions
gemini-cli switch-session abc123
```

**Pros**: Simple, discoverable, shorter to type  
**Cons**: Can get cluttered with many commands

### Option B: Nested Subcommands (Recommended for complex CLIs)

```bash
gemini-cli upload photo.jpg
gemini-cli ask "What's in this image?"
gemini-cli session list
gemini-cli session switch abc123
gemini-cli session delete abc123
gemini-cli config show
gemini-cli auth verify
```

**Pros**: Organized, scalable, logical grouping  
**Cons**: More typing, harder discovery

### Option C: Hybrid Approach

Top-level for frequent commands, nested for less common:

```bash
# Frequent (top-level)
gemini-cli upload photo.jpg
gemini-cli ask "What's in this image?"

# Less frequent (nested)
gemini-cli session list
gemini-cli config show
```

---

## Global Flags

### Recommended Global Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--config` | `-c` | Config file path | `~/.gemini-media-cli/config.yaml` |
| `--verbose` | `-v` | Verbose output | `false` |
| `--quiet` | `-q` | Suppress non-essential output | `false` |
| `--output` | `-o` | Output format: text, json, markdown | `text` |
| `--no-color` | | Disable colored output | `false` |
| `--timeout` | | Request timeout | `2m` |
| `--help` | `-h` | Show help | |
| `--version` | | Show version | |

### Flag Placement

```bash
# Global flags before subcommand
gemini-cli --verbose upload photo.jpg

# Or after (both should work)
gemini-cli upload photo.jpg --verbose
```

---

## Output Formats

### Text Output (Default)

```
$ gemini-cli session list

Sessions
────────
  abc123  (active)  Created: 2025-12-30  Files: 3
  def456            Created: 2025-12-29  Files: 1
  ghi789            Created: 2025-12-28  Files: 5

Total: 3 sessions
```

### JSON Output

```bash
$ gemini-cli session list --output json
```

```json
{
  "sessions": [
    {"id": "abc123", "active": true, "created": "2025-12-30", "files": 3},
    {"id": "def456", "active": false, "created": "2025-12-29", "files": 1}
  ],
  "total": 2
}
```

### Markdown Output

```bash
$ gemini-cli ask "What's in this image?" --output markdown
```

Renders response with markdown formatting.

---

## Interactive Mode

### Design Options

#### Option A: REPL Style

```
$ gemini-cli interactive

gemini> upload photo.jpg
✓ Uploaded: files/abc123

gemini> What's in this image?
The image shows a sunset over the ocean...

gemini> Can you identify the location?
Based on the coastline, this appears to be...

gemini> exit
Goodbye!
```

#### Option B: Chat Style

```
$ gemini-cli chat

Welcome to Gemini Media CLI
Type /help for commands, /exit to quit

You: /upload photo.jpg
✓ Uploaded: files/abc123

You: What's in this image?
Gemini: The image shows a sunset over the ocean...

You: /exit
```

### Interactive Commands

| Command | Description |
|---------|-------------|
| `/upload <file>` | Upload a file |
| `/files` | List uploaded files |
| `/clear` | Clear current session |
| `/session <id>` | Switch session |
| `/history` | Show conversation history |
| `/export` | Export session |
| `/help` | Show help |
| `/exit` or `/quit` | Exit interactive mode |

---

## Progress & Feedback

### Spinner Styles

```
⠋ Uploading photo.jpg...          # Dots spinner
◐ Uploading photo.jpg...          # Circle spinner
▰▱▱▱▱ Uploading photo.jpg...      # Bar spinner
```

### Progress Bar (for uploads)

```
Uploading photo.jpg
[████████████░░░░░░░░] 60% | 1.2 MB/s | ETA: 5s
```

### Success/Error Indicators

```
✓ Upload complete: photo.jpg
✗ Upload failed: connection timeout
⚠ Warning: Large file may take a while
ℹ Info: Using model gemini-2.0-flash-exp
```

---

## Error Display

### Standard Error Format

```
Error: File not found: photo.jpg

The file 'photo.jpg' does not exist at the specified path.

Suggestions:
  • Check the file path is correct
  • Use an absolute path: /Users/you/photos/photo.jpg
  • Run 'ls' to list files in current directory
```

### Compact Error (with --quiet)

```
Error: File not found: photo.jpg
```

### JSON Error (with --output json)

```json
{
  "error": {
    "type": "file_not_found",
    "message": "File not found: photo.jpg",
    "details": {"path": "photo.jpg"}
  }
}
```

---

## Help System

### Command Help

```
$ gemini-cli upload --help

Upload a media file to Gemini for analysis

Usage:
  gemini-cli upload <file> [flags]

Arguments:
  file    Path to the image or video file

Flags:
  -s, --session string   Session ID to add file to (default: active session)
      --no-analyze       Upload without initial analysis
  -h, --help             Help for upload

Examples:
  gemini-cli upload photo.jpg
  gemini-cli upload video.mp4 --session abc123
  gemini-cli upload ~/Pictures/*.jpg
```

### Short vs Long Help

```bash
gemini-cli --help           # Short overview
gemini-cli help upload      # Detailed command help
gemini-cli upload --help    # Same as above
```

---

## Confirmation Prompts

### Destructive Actions

```
$ gemini-cli session delete abc123

This will permanently delete session abc123 and all its history.
Are you sure? [y/N]: y

✓ Session deleted
```

### Skip Confirmation

```bash
gemini-cli session delete abc123 --yes
gemini-cli session delete abc123 -y
```

### Non-Interactive Mode

When stdin is not a TTY, assume `--yes` or fail:

```bash
echo "abc123" | gemini-cli session delete --yes
```

---

## Tab Completion

### Bash Completion

```bash
# Add to ~/.bashrc
eval "$(gemini-cli completion bash)"
```

### Zsh Completion

```bash
# Add to ~/.zshrc
eval "$(gemini-cli completion zsh)"
```

### Completions to Support

- Command names
- Flag names
- Session IDs
- File paths (with MIME type filtering)

---

## Color Usage

| Element | Color | Purpose |
|---------|-------|---------|
| Success | Green | ✓ confirmations |
| Error | Red | ✗ failures |
| Warning | Yellow | ⚠ warnings |
| Info | Blue | ℹ information |
| Prompt | Cyan | User input prompts |
| Dim | Gray | Secondary info, timestamps |

### Disable Colors

```bash
gemini-cli --no-color upload photo.jpg
GEMINI_COLOR=never gemini-cli upload photo.jpg
NO_COLOR=1 gemini-cli upload photo.jpg  # Standard
```

---

## Keyboard Shortcuts (Interactive)

| Key | Action |
|-----|--------|
| `Ctrl+C` | Cancel current operation |
| `Ctrl+D` | Exit interactive mode |
| `↑/↓` | History navigation |
| `Tab` | Auto-complete |
| `Ctrl+L` | Clear screen |
| `Ctrl+R` | Search history |

---

## Piping & Scripting

### Pipe-Friendly Output

```bash
# Pipe file list
gemini-cli session list --output json | jq '.sessions[].id'

# Pipe response
gemini-cli ask "Summarize this" | head -n 5

# Script usage
for file in *.jpg; do
  gemini-cli upload "$file" --quiet
done
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Invalid arguments |
| 3 | Authentication error |
| 4 | Network error |
| 5 | File error |
| 130 | Interrupted (Ctrl+C) |

---

## Design Decisions To Make

1. **Command structure**: Flat, nested, or hybrid?
2. **Interactive prompt style**: REPL or chat?
3. **Spinner style**: Dots, circle, or bar?
4. **Color scheme**: Default terminal or custom theme?
5. **Confirmation behavior**: Always prompt or --yes flag?
6. **Default output format**: Text, JSON, or markdown?
7. **Progress display**: Spinner only or progress bar for uploads?

---

**Last Updated**: 2025-12-30  
**Version**: 1.0.0


# DDR-015: CLI Directory Arguments with Cobra

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 8

## Context

The CLI currently uses hardcoded directory paths for testing. To make the tool usable, we need proper command-line argument parsing. Users should be able to specify a directory path via flags, with sensible defaults and interactive prompting for a better UX.

Key requirements:
- Directory path via command-line flag
- Interactive prompting when no argument provided
- Recursive directory scanning by default
- Support for limiting results and recursion depth
- Help documentation

## Decision

Use **Cobra** as the CLI framework with the following flag structure:

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--directory` | `-d` | string | "" | Directory containing images to analyze |
| `--max-depth` | | int | 0 | Maximum recursion depth (0 = unlimited) |
| `--limit` | | int | 0 | Maximum images to process (0 = unlimited) |
| `--help` | `-h` | | | Display help message |

### Behavior

1. **With `--directory` flag**: Use specified directory
2. **Without flag**: Prompt interactively with current directory as default
3. **Interactive prompt format**: `Directory [/current/path]: `
4. **Empty input at prompt**: Use current working directory

### Directory Scanning

- **Recursive by default**: Scan all subdirectories
- **Symlink handling**: Follow symlinks to files, skip symlinks to directories (prevents infinite loops)
- **Hidden files**: Include all files (no filtering of dotfiles)
- **Alphabetical ordering**: Files sorted by path for consistent results

## Rationale

### Why Cobra?

- **Industry standard**: Used by kubectl, docker, gh, hugo, and most popular Go CLIs
- **Rich feature set**: Subcommands, flags, auto-generated help, shell completion
- **Future-proof**: Easy to add subcommands as the CLI grows
- **Documentation**: Auto-generates usage text from flag definitions

### Why `-d` as short flag?

- Widely recognized for directory (tar, cp, mkdir use it)
- Intuitive mnemonic: **d**irectory
- Reserved `-D` for potential future `--debug` flag

### Why interactive prompting?

- Better UX than error messages
- Reduces friction for casual use
- Default to current directory minimizes typing
- Follows modern CLI patterns (gh, gcloud)

### Why recursive by default?

- Photo directories often have date-based subdirectories
- Users expect "analyze this folder" to include subfolders
- `--max-depth 1` available for flat-only scanning

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Positional argument | Less explicit; harder to extend with future commands |
| Go standard `flag` package | Limited features; no subcommand support for future |
| urfave/cli | Less popular; Cobra has better ecosystem |
| Error on missing argument | Poor UX; interactive prompting is friendlier |
| Non-recursive by default | Photo workflows typically use subdirectories |

## Consequences

**Positive:**
- Industry-standard CLI experience
- Self-documenting with `--help`
- Easy to extend with subcommands later
- Interactive mode improves UX

**Trade-offs:**
- Adds Cobra dependency (~2MB to binary)
- Slightly more complex code structure than raw flag parsing

## Usage Examples

```bash
# Explicit directory
gemini-cli --directory /path/to/photos
gemini-cli -d ./vacation-photos

# Interactive mode
gemini-cli
# Output: Directory [/current/path]: <user types or presses Enter>

# With limits
gemini-cli -d ./photos --max-depth 2 --limit 50

# Help
gemini-cli --help
```

## Related Documents

- [DDR-001: Iterative Implementation](./DDR-001-iterative-implementation.md) - Iteration 8 is part of Media Uploads phase
- [DDR-014: Thumbnail Selection Strategy](./DDR-014-thumbnail-selection-strategy.md) - Directory scanning feeds into photo selection
- [cli_ux.md](../cli_ux.md) - CLI design patterns and conventions


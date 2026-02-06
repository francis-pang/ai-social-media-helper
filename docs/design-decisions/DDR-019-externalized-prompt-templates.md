# DDR-019: Externalized Prompt Templates

**Date**: 2026-02-06  
**Status**: Accepted  
**Iteration**: 12

## Context

The application currently hardcodes all AI prompt strings as Go `const` values and inline string literals within Go source files (`internal/chat/chat.go` and `internal/chat/selection.go`). There are five distinct prompt texts totaling ~200 lines of prose:

1. `SystemInstruction` — media analysis system instruction (6 lines)
2. `SelectionSystemInstruction` — photo selection system instruction (50 lines)
3. `buildSocialMediaImagePrompt` — image analysis prompt (60 lines)
4. `buildSocialMediaVideoPrompt` — video analysis prompt (70 lines)
5. `buildGenericSocialMediaPrompt` — generic fallback prompt (10 lines)

As the application matures, prompts will evolve **faster** than the surrounding Go code. Prompt engineering iteration—tweaking wording, adjusting output format requirements, refining selection criteria—does not require changes to program logic, API call mechanics, or data structures. Yet the current approach forces every prompt revision to touch Go source files, making diffs noisy and conflating content changes with code changes.

## Decision

Extract all prompt text into standalone text files under `internal/assets/prompts/` and load them at compile time using Go's `//go:embed` directive. Prompts that require dynamic data (e.g., metadata context) use Go `text/template` with named placeholders.

### File Structure

```
internal/assets/
├── assets.go                          # Existing (reference photo)
├── prompts.go                         # NEW: embed directives + template helpers
├── prompts/
│   ├── system-instruction.txt         # Media analysis system instruction
│   ├── selection-system.txt           # Photo selection system instruction
│   ├── social-media-image.txt         # Image prompt template (uses {{.MetadataContext}})
│   ├── social-media-video.txt         # Video prompt template (uses {{.MetadataContext}})
│   └── social-media-generic.txt       # Generic prompt template (uses {{.MetadataContext}})
└── reference-photos/
    └── francis-reference.jpg          # Existing
```

### Template Convention

- Static prompts (no dynamic data): embedded as raw `string` via `//go:embed`
- Dynamic prompts (with metadata injection): embedded as `string`, parsed with `text/template` at call time
- Template variables use Go template syntax: `{{.MetadataContext}}`
- Template rendering is encapsulated in helper functions in `prompts.go`

## Rationale

1. **Separation of concerns** — Prompt content (what to say) is separated from program logic (how to call the API). Domain experts can review and revise prompts without reading Go code.
2. **Clean diffs** — A prompt wording change shows up as a `.txt` file diff, not buried in a `.go` source change. Code reviewers can distinguish "prompt tuning" commits from "logic change" commits at a glance.
3. **Faster iteration** — Editing a `.txt` file and rebuilding is lower friction than navigating string concatenation in Go. IDE syntax highlighting and spell-check work properly on plain text files.
4. **Consistent with existing pattern** — The codebase already uses `//go:embed` for the Francis reference photo (`internal/assets/assets.go`). Extending this pattern to prompts is natural and idiomatic.
5. **Single binary preserved** — `//go:embed` compiles the text files into the binary. No runtime file dependencies, no deployment complexity.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| External config files (YAML/JSON) loaded at runtime | Breaks single-binary distribution model; requires file management alongside the binary |
| Environment variables for prompts | Impractical for multi-line prompt text; poor ergonomics |
| Database-stored prompts | Over-engineering for a CLI tool; adds infrastructure dependency |
| Keep prompts hardcoded in Go | Mixes concerns; noisy diffs; poor editing ergonomics for large prompt blocks |
| `embed.FS` with directory embedding | Unnecessary complexity; individual `//go:embed` is simpler for a known set of files |

## Consequences

**Positive:**
- Prompt changes produce clean, reviewable text-only diffs
- Prompts can be edited with any text editor, with full spell-check and formatting support
- Natural path toward future enhancements: prompt versioning, A/B testing, user-customizable prompts
- Go code becomes shorter and focused on logic rather than string construction
- Template variables are explicitly documented in each prompt file
- Consistent architectural pattern with existing embedded assets

**Trade-offs:**
- Still requires recompilation after prompt changes (acceptable for current use case; runtime loading could be added later if needed)
- Template syntax errors are caught at runtime rather than compile time (mitigated by `template.Must` causing immediate panic on malformed templates)
- Slight additional complexity from `text/template` parsing (negligible cost; templates are parsed once)

## Implementation

### Prompt Files

Each prompt file contains the full prompt text. For templates with dynamic sections, Go template syntax marks insertion points:

```
## About the Person
The person in this image is Francis, the owner of this photo.

{{if .MetadataContext}}{{.MetadataContext}}
{{end}}## Your Task
...
```

### Embed and Render (prompts.go)

```go
//go:embed prompts/system-instruction.txt
var SystemInstructionPrompt string

//go:embed prompts/social-media-image.txt
var socialMediaImageTemplate string

func RenderSocialMediaImagePrompt(metadataContext string) string {
    tmpl := template.Must(template.New("image").Parse(socialMediaImageTemplate))
    var buf bytes.Buffer
    tmpl.Execute(&buf, PromptData{MetadataContext: metadataContext})
    return buf.String()
}
```

### Chat Package Refactoring

`chat.go` and `selection.go` replace inline prompt strings with calls to `assets.RenderXxxPrompt()`. The `BuildPhotoSelectionPrompt` function retains its dynamic metadata-per-photo loop in Go code but uses the embedded template for the static preamble and output format sections.

## Related Decisions

- DDR-007: Hybrid Prompt Strategy for EXIF Metadata (established prompt design pattern)
- DDR-016: Quality-Agnostic Photo Selection (defined selection prompt content)
- DDR-017: Francis Reference Photo (established `//go:embed` pattern in `internal/assets/`)

# Architecture Overview

## Purpose

The Gemini Media Analysis CLI enables users to:
- Upload media files (images/videos) directly to Gemini API, bypassing typical UI file size limits
- Maintain stateful conversation sessions for asking multiple questions about uploaded media
- Perform in-depth analysis of visual content using Gemini's multimodal capabilities

## Language Choice: Go

**Selected for:**
- ⚡ **Fast startup times** - Native binary, no JVM overhead
- 📦 **Single binary deployment** - Easy distribution, no dependencies
- 🚀 **Efficient file handling** - Excellent streaming support for large files
- 🛠️ **CLI-first design** - Strong ecosystem for command-line tools
- 🔒 **Type safety** - Compile-time error checking
- 🧵 **Concurrency** - Built-in goroutines for efficient concurrent operations

See [language_comparison.md](./language_comparison.md) for detailed comparison with alternatives.

---

## Core Components

1. **CLI Binaries** - Independent commands under `cmd/`:
   - `media-select` - AI-powered media selection for Instagram carousels
   - `media-triage` - AI-powered media triage to identify and delete unsaveable files
   - `media-web` - Local web server providing a visual UI for triage and selection (Phase 1)
   - `media-lambda` - AWS Lambda function for cloud-hosted triage via S3 (Phase 2)
2. **Web Frontend** - Preact SPA under `web/frontend/` consumed by both the local web server and CloudFront
3. **File Handler** - File validation, EXIF extraction, thumbnail generation
4. **Gemini Client** - API communication and file uploads
5. **Photo Selection** - Quality-agnostic selection with scene detection
6. **Media Triage** - Batch quality/meaning evaluation with interactive deletion
7. **Session Manager** - Stateful conversation management (future)
8. **Configuration** - API key and settings management

---

## Technical Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| **Language** | Go 1.24+ | Core language |
| **Gemini Model** | `gemini-3-flash-preview` | AI model (free tier compatible) |
| **SDK** | `github.com/google/generative-ai-go/genai` | Official Gemini API SDK |
| **Logging** | `github.com/rs/zerolog` | Structured logging |
| **CLI Framework** | `github.com/spf13/cobra` | Command-line interface |
| **Web Frontend** | Preact + Vite + TypeScript | Browser-based UI (SPA) |
| **Web Server** | Go `net/http` + `embed.FS` | Local JSON API + embedded SPA (Phase 1) |
| **Lambda Adapter** | `aws-lambda-go-api-proxy` | API Gateway HTTP API v2 to `http.ServeMux` (Phase 2) |
| **AWS SDK** | `aws-sdk-go-v2` (S3, SSM) | S3 presigned URLs, object operations, secrets (Phase 2) |
| **Configuration** | Environment variables + GPG (local), SSM Parameter Store (cloud) | Config and secret management |
| **JSON** | `encoding/json` | Session persistence |
| **File I/O** | `os`, `io`, `mime` | File handling |
| **UUID** | `github.com/google/uuid` | Session IDs |
| **Testing** | `testing` package | Unit tests |
| **Build** | `go build` | Single binary output |
| **Dependencies** | `go.mod` + `go.sum` | Dependency management |

**Pricing Reference**: [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing)

---

## Data Flow

```
┌─────────────────┐
│  User Input     │  (CLI commands)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Cobra Parser    │  (cmd/media-select/ or cmd/media-triage/)
│ - Parse args    │
│ - Route commands│
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ File Handler    │  (internal/filehandler/handler.go)
│ - Validate      │
│ - Read file     │
│ - Stream I/O    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Gemini Client   │  (internal/gemini/client.go)
│ - Upload file   │
│ - Generate      │
│ - Context-aware │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Session Manager │  (internal/session/manager.go)
│ - Store refs    │
│ - Save history  │
│ - Persist JSON  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Response Output │  (fmt.Printf)
└─────────────────┘
```

### Go-Specific Characteristics

- ✅ Context propagation for cancellation/timeouts
- ✅ Error handling via `(result, error)` tuples
- ✅ Thread-safe operations with mutexes
- ✅ Streaming file I/O for large files
- ✅ No shared mutable state

---

## Photo Selection Flow (Iteration 10)

```
┌─────────────────┐
│ Directory Scan  │  Recursive, sorted by path
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ EXIF Extraction │  GPS, Date, Camera info
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Thumbnail Gen   │  1024px max, JPEG output
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Gemini API      │  Thumbnails + Metadata + Context
│ Selection       │
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│ Structured Output                        │
│ 1. Ranked list with justification       │
│ 2. Scene grouping (hybrid detection)    │
│ 3. Exclusion report (per-photo reasons) │
└─────────────────────────────────────────┘
```

### Quality-Agnostic Selection

**Key Principle**: Photo quality is NOT a selection criterion. User has Google enhancement tools (Magic Editor, Unblur, Portrait Light, etc.).

**Selection Priorities**:
1. Subject/Scene Diversity (Highest)
2. Scene Representation
3. Enhancement Potential (duplicates only)
4. People Variety (Lower)
5. Time of Day (Tiebreaker)

**Scene Detection (Hybrid)**:
- Visual similarity
- Time gaps (2+ hours = new scene)
- GPS gaps (1km+ = new location)

See [DDR-016](./design-decisions/DDR-016-quality-agnostic-photo-selection.md) for details.

---

## Media Triage Flow (Iteration 12)

```
┌─────────────────┐
│ Directory Scan  │  Recursive, images + videos
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Pre-filter      │  Videos < 2s flagged locally
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Media Processing│  Thumbnails (images) + Compress (videos)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Gemini API      │  Single batch call with all media
│ Triage          │  Returns JSON array of verdicts
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│ Interactive Report                       │
│ 1. KEEP list with reasons               │
│ 2. DISCARD list with reasons             │
│ 3. Confirm deletion prompt               │
└─────────────────────────────────────────┘
```

### Triage Criteria

**Key Principle**: Be generous — if a normal person can understand the subject and light editing could make it decent, keep it.

**Discard if:**
- Too dark/blurry to recover any meaningful content
- Accidental shot (pocket photo, floor, finger over lens)
- No discernible subject or meaning
- Video too short (< 2 seconds, pre-filtered locally)

See [DDR-021](./design-decisions/DDR-021-media-triage-command.md) for details.

---

## Web UI Architecture (Phase 1)

```
┌──────────────────────────────────────────────────┐
│  Browser                                          │
│  ┌────────────────────────────────────────────┐  │
│  │ Preact SPA                                  │  │
│  │  - File browser (directory listing)         │  │
│  │  - Thumbnail grid (media preview)           │  │
│  │  - Multi-select & confirm (triage actions)  │  │
│  └──────────────┬─────────────────────────────┘  │
│                  │ fetch("/api/...")               │
└──────────────────┼───────────────────────────────┘
                   │
┌──────────────────┼───────────────────────────────┐
│  Go HTTP Server  │  (cmd/media-web)               │
│                  ▼                                 │
│  ┌──────────────────────────┐  ┌───────────────┐ │
│  │ JSON REST API             │  │ Static Files  │ │
│  │  /api/browse              │  │ (embed.FS)    │ │
│  │  /api/triage/start        │  │ index.html    │ │
│  │  /api/triage/{id}/results │  │ JS/CSS        │ │
│  │  /api/triage/{id}/confirm │  └───────────────┘ │
│  │  /api/media/thumbnail     │                     │
│  │  /api/media/full          │                     │
│  └──────────┬───────────────┘                     │
│             │                                      │
│  ┌──────────▼───────────────┐                     │
│  │ internal/ packages        │                     │
│  │  filehandler, chat, auth  │                     │
│  └──────────┬───────────────┘                     │
│             │                                      │
└─────────────┼──────────────────────────────────────┘
              │
              ▼
┌──────────────────────┐  ┌─────────────────┐
│ Local Filesystem     │  │ Gemini API      │
│ (media files)        │  │ (AI evaluation) │
└──────────────────────┘  └─────────────────┘
```

**Key design principle:** The Go server only serves JSON. The Preact SPA handles all rendering. This clean separation enabled the migration to AWS Lambda (Phase 2) without changing the frontend.

See [DDR-022](./design-decisions/DDR-022-web-ui-preact-spa.md) for the full decision record.

---

## Cloud Architecture (Phase 2)

Phase 2 migrates the application from a local tool to a remotely hosted service. The Preact SPA is deployed to CloudFront, the Go backend runs as a Lambda function, and media files are stored in S3.

```
┌──────────────────────────────────────────────────────────────────────┐
│  Browser                                                              │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │ Preact SPA (VITE_CLOUD_MODE=1)                                 │  │
│  │  - Drag-and-drop file upload (FileUploader)                    │  │
│  │  - Presigned URL upload directly to S3                         │  │
│  │  - Thumbnail grid (media preview via /api/media/thumbnail)     │  │
│  │  - Multi-select & confirm (triage actions)                     │  │
│  └──────────────┬────────────────────────────┬────────────────────┘  │
│                  │ fetch("/api/...")           │ PUT (presigned URL)   │
└──────────────────┼────────────────────────────┼──────────────────────┘
                   │                            │
┌──────────────────┼────────────────────────────┼──────────────────────┐
│  CloudFront      │  (d10rlnv7vz8qt7.cloudfront.net)                  │
│                  │                            │                       │
│  ┌───────────────▼───────────────┐            │                       │
│  │ /api/* behavior               │            │                       │
│  │ (proxy to API Gateway)        │            │                       │
│  └───────────────┬───────────────┘            │                       │
│  ┌───────────────────────────────┐            │                       │
│  │ /* behavior (default)         │            │                       │
│  │ S3 origin (OAC, cached)       │            │                       │
│  └───────────────────────────────┘            │                       │
└──────────────────┼────────────────────────────┼──────────────────────┘
                   │                            │
┌──────────────────▼───────────────┐  ┌─────────▼──────────────────────┐
│  API Gateway HTTP API            │  │  S3 Media Bucket               │
│  /api/{proxy+} -> Lambda         │  │  ai-social-media-uploads-...   │
└──────────────────┬───────────────┘  │  {sessionId}/{filename}        │
                   │                  │  24h auto-expiration            │
┌──────────────────▼───────────────┐  └────────────────────────────────┘
│  Lambda (provided.al2023)        │
│  cmd/media-lambda/main.go        │
│  ┌─────────────────────────────┐ │
│  │ httpadapter.NewV2 (ServeMux)│ │
│  │  /api/health                │ │
│  │  /api/upload-url            │ │
│  │  /api/triage/start          │ │
│  │  /api/triage/{id}/results   │ │
│  │  /api/triage/{id}/confirm   │ │
│  │  /api/media/thumbnail       │ │
│  │  /api/media/full            │ │
│  └──────────┬──────────────────┘ │
│             │ reuses internal/   │
│  ┌──────────▼──────────────────┐ │
│  │ chat.AskMediaTriage()       │ │
│  │ filehandler.LoadMediaFile() │ │
│  │ filehandler.GenerateThumbnail()│
│  └──────────┬──────────────────┘ │
└─────────────┼────────────────────┘
              │
    ┌─────────▼─────────┐  ┌───────────────────────┐
    │ Gemini API         │  │ SSM Parameter Store   │
    │ (AI evaluation)    │  │ (Gemini API key)      │
    └────────────────────┘  └───────────────────────┘
```

### Key Design Decisions

1. **Presigned URL uploads** bypass Lambda's 6MB payload limit — the browser uploads directly to S3
2. **Session-based grouping** — each upload session gets a UUID; files are stored at `{sessionId}/{filename}` in S3
3. **Download-to-tmp processing** — Lambda downloads S3 objects to `/tmp` so existing `filehandler` and `chat` packages work unchanged
4. **CloudFront API proxy** — `/api/*` requests are proxied to API Gateway, making all requests same-origin (no CORS)
5. **Build-time mode detection** — `VITE_CLOUD_MODE` env var determines whether the SPA shows the file uploader (cloud) or file picker (local)
6. **Separate binary** — `cmd/media-lambda` is purpose-built for Lambda rather than sharing handlers with `cmd/media-web` via a StorageProvider interface, because the two modes have fundamentally different I/O patterns

### AWS Resources

| Resource | Purpose |
|----------|---------|
| S3 (media uploads) | Stores uploaded media files (24h auto-expiration) |
| S3 (frontend) | Stores Preact SPA static assets |
| CloudFront | Serves frontend + proxies `/api/*` to API Gateway |
| API Gateway HTTP API | Routes requests to Lambda (JWT authorizer) |
| Lambda (`provided.al2023`) | Go binary handling API requests |
| Cognito User Pool | Authentication (DDR-028) |
| SSM Parameter Store | Gemini API key (SecureString) |
| CodePipeline | CI/CD: GitHub source -> Go + Node builds -> S3 + Lambda deploy |

See [DDR-026](./design-decisions/DDR-026-phase2-lambda-s3-deployment.md) for the full decision record.
See [DDR-028](./design-decisions/DDR-028-security-hardening.md) for the security hardening decision record.
See [PHASE2-REMOTE-HOSTING.md](./PHASE2-REMOTE-HOSTING.md) for the hosting platform evaluation.

---

## Security Architecture (DDR-028)

The cloud deployment is hardened with defense-in-depth security:

```
Browser ──► CloudFront ──► API Gateway ──► Lambda
  │              │               │            │
  │  x-origin-verify     JWT Authorizer  Origin verify
  │  custom header       (Cognito)       middleware
  │                                          │
  │              Input validation ◄──────────┘
  │              (sessionId, filename, S3 key,
  │               content type, file size)
```

| Layer | Control | Purpose |
|-------|---------|---------|
| CloudFront | Origin-verify header | Blocks direct API Gateway access |
| API Gateway | JWT authorizer (Cognito) | Authentication |
| API Gateway | Default throttling (100 burst / 50 rps) | Rate limiting / DoS protection |
| API Gateway | CORS locked to CloudFront domain | Cross-origin restriction |
| Lambda | Origin-verify middleware | Defense-in-depth for header check |
| Lambda | Input validation (UUID, filename regex) | Injection / traversal prevention |
| Lambda | Content-type allowlist + size limits | Upload abuse prevention |
| Lambda | Random job IDs (crypto/rand) | Enumeration prevention |
| Lambda | Safe error messages | Information leak prevention |
| S3 | CORS locked to CloudFront domain | Cross-origin restriction |

---

## Media Selection Flow (Cloud Mode — DDR-029)

Cloud mode is being extended from a triage-only workflow to a full media selection and publishing pipeline. The selection flow runs in cloud mode while the triage flow continues to operate in local mode unchanged.

### Step 1: Upload Media

The upload step uses the **File System Access API** (Chrome-only) for a native file/folder picking experience:

```
┌──────────────────────────────────────────────────┐
│  Browser (Chrome on macOS)                        │
│                                                    │
│  ┌──────────────────────────────────────────────┐ │
│  │ MediaUploader Component                       │ │
│  │                                                │ │
│  │ [Choose Files]  [Choose Folder]               │ │
│  │  showOpenFilePicker()  showDirectoryPicker()  │ │
│  │                                                │ │
│  │ ┌─────────┐ ┌─────────┐ ┌─────────┐          │ │
│  │ │ Thumb 1 │ │ Thumb 2 │ │ Thumb N │  ← Canvas│ │
│  │ └─────────┘ └─────────┘ └─────────┘          │ │
│  │ ═══════════════════ 67% ═══════    ← S3 PUT  │ │
│  │                                                │ │
│  │ Trip context: [3-day trip to Tokyo...]        │ │
│  │                     [Continue to Selection →] │ │
│  └──────────────────────────────────────────────┘ │
└────────────────────────┬─────────────────────────┘
                         │ PUT (presigned URL)
                         ▼
┌────────────────────────────────────────────────────┐
│  S3 Media Bucket                                    │
│  {sessionId}/IMG_001.jpg                            │
│  {sessionId}/IMG_002.png                            │
│  {sessionId}/VID_003.mp4                            │
└────────────────────────────────────────────────────┘
```

**Key features:**

- **File System Access API** — `showOpenFilePicker()` for individual files, `showDirectoryPicker()` with recursive scanning for folders
- **Client-side thumbnails** — generated in-browser using `<canvas>` for images and `<video>` frame extraction for videos
- **Media filtering** — folder picker iterates entries and only collects supported media types (JPEG, PNG, GIF, WebP, HEIC, MP4, MOV, etc.)
- **Trip context** — text input describing the event (used by AI selection in later steps)
- **Drag-and-drop** — retained as supplementary input method alongside the API buttons
- **S3 upload** — reuses existing presigned PUT URL flow (no backend changes)

See [DDR-029](./design-decisions/DDR-029-file-system-access-api-upload.md) for the full decision record.

### Planned Steps (Not Yet Implemented)

2. **AI Selection** — Gemini analyzes all uploaded media and selects the best items
3. **Review Selection** — User reviews AI choices, can override selections
4. **Enhancement** — AI-powered photo editing and video adjustment
5. **Review Enhanced** — Side-by-side comparison with feedback loop
6. **Group into Posts** — Drag-and-drop media into post groups (max 20 per post)
7. **Publish or Download** — Instagram carousel publishing or file download
8. **Post Description** — AI-generated Instagram captions with iterative feedback

### Backend: Multi-Lambda + Step Functions Architecture (Planned)

Steps 2+ require long-running parallel processing that cannot fit within API Gateway's 30-second timeout. The architecture splits into two orchestration patterns:

1. **User-driven transitions** (upload -> review -> enhance -> group -> publish): Managed via DynamoDB state + frontend polling. The user controls the pace.
2. **Within-step parallel processing** (generate N thumbnails, enhance N photos): Managed via **AWS Step Functions** Map state with built-in retry, concurrency throttling, and fan-in.

```
CloudFront ──► API Gateway ──► API Lambda ──► DynamoDB
                                    │
                                    ├──► Step Functions: SelectionPipeline
                                    │       ├── Map: Thumbnail Lambda (per file, MaxConcurrency 20)
                                    │       └── Selection Lambda (Gemini AI)
                                    │
                                    └──► Step Functions: EnhancementPipeline
                                            ├── Map: Enhancement Lambda (per photo, MaxConcurrency 10)
                                            └── Map: Video Lambda (per video, MaxConcurrency 5)
```

**Lambda functions (planned):**

| Lambda | Purpose | Memory | Timeout |
|--------|---------|--------|---------|
| API Lambda (existing) | HTTP API, DynamoDB R/W, presigned URLs, start Step Functions | 256 MB | 30s |
| Thumbnail Lambda | Per-file thumbnail generation (image resize / video frame) | 512 MB | 2 min |
| Selection Lambda | Gemini AI media selection (all thumbnails + metadata) | 2-4 GB | 15 min |
| Enhancement Lambda | Per-photo Gemini image editing | 1-2 GB | 5 min |
| Video Processing Lambda | Per-video ffmpeg enhancement (optional) | 4+ GB | 15 min |

**Step Functions state machines (planned):**

| State Machine | Trigger | Flow |
|---------------|---------|------|
| `SelectionPipeline` | `POST /api/selection/start` | List S3 files -> Map: generate thumbnails (parallel) -> Selection Lambda (Gemini) -> Write to DynamoDB |
| `EnhancementPipeline` | `POST /api/enhance/start` | Split photos/videos -> Parallel: Map enhance photos + Map process videos -> Aggregate -> Write to DynamoDB |

Step Functions provides built-in parallel execution, per-item retry with backoff, concurrency throttling (`MaxConcurrency`), fan-in (wait for all), and visual execution monitoring. Cost is ~$0.002 per session (~$0.60/month at 10 sessions/day).

**Infrastructure (planned):**

| Resource | Purpose |
|----------|---------|
| DynamoDB (`media-selection-sessions`) | Single-table session state with TTL auto-cleanup |
| Step Functions (2 state machines) | Orchestrate parallel thumbnail generation and enhancement |
| SSM Parameter Store (new keys) | Instagram access token and user ID |

---

## Frontend Components

| Component | Mode | Purpose |
|-----------|------|---------|
| `FileBrowser.tsx` | Local | Native OS file picker via Go backend |
| `FileUploader.tsx` | Cloud (triage) | Drag-and-drop S3 upload for triage flow |
| `MediaUploader.tsx` | Cloud (selection) | File System Access API pickers, thumbnails, trip context (DDR-029) |
| `LoginForm.tsx` | Cloud | Cognito authentication UI (DDR-028) |
| `SelectedFiles.tsx` | Both | File selection confirmation |
| `TriageView.tsx` | Both | Triage results and deletion interface |

---

## Future Extensibility

### Potential Enhancements

1. **Custom domain** — ACM certificate + Route 53 for a friendly URL
2. **AWS WAF** — Web Application Firewall with managed rule sets (~$6-8/mo, see DDR-028)
3. **Google Drive storage provider** — Triage media already uploaded to Google Drive without re-downloading

---

**Last Updated**: 2026-02-08
**Updated for**: DDR-029 (File System Access API for Cloud Media Upload)


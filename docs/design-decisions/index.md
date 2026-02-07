# Design Decisions

Historical records of design decisions made during development.

Design decisions are **append-only** records documenting why decisions were made at specific points in time, including context, alternatives considered, and trade-offs accepted.

For new decisions, use [design_template.md](./design_template.md).

---

## Decision Log

| ID | Date | Title | Status |
|----|------|-------|--------|
| [DDR-001](./DDR-001-iterative-implementation.md) | 2025-12-30 | Iterative Implementation Approach | Accepted |
| [DDR-002](./DDR-002-logging-before-features.md) | 2025-12-30 | Logging Infrastructure First | Accepted |
| [DDR-003](./DDR-003-gpg-credential-storage.md) | 2025-12-30 | GPG Credential Storage | Accepted |
| [DDR-004](./DDR-004-startup-api-validation.md) | 2025-12-30 | Startup API Key Validation | Accepted |
| [DDR-005](./DDR-005-typed-validation-errors.md) | 2025-12-30 | Typed Validation Errors | Accepted |
| [DDR-006](./DDR-006-model-selection.md) | 2025-12-30 | Model Selection: gemini-3-flash-preview | Accepted |
| [DDR-007](./DDR-007-hybrid-exif-prompt.md) | 2025-12-31 | Hybrid Prompt Strategy for EXIF Metadata | Accepted |
| [DDR-008](./DDR-008-pure-go-exif-library.md) | 2025-12-31 | Pure Go EXIF Library | Accepted |
| [DDR-009](./DDR-009-gemini-reverse-geocoding.md) | 2025-12-31 | Gemini Native Reverse Geocoding | Accepted |
| [DDR-010](./DDR-010-heic-format-support.md) | 2025-12-31 | HEIC/HEIF Image Format Support | Accepted |
| [DDR-011](./DDR-011-video-metadata-and-upload.md) | 2025-12-31 | Video Metadata Extraction and Large File Upload | Accepted |
| [DDR-012](./DDR-012-files-api-for-all-uploads.md) | 2025-12-31 | Files API for All Media Uploads | Accepted |
| [DDR-013](./DDR-013-unified-metadata-architecture.md) | 2025-12-31 | Unified Metadata Extraction Architecture | Accepted |
| [DDR-014](./DDR-014-thumbnail-selection-strategy.md) | 2025-12-31 | Thumbnail-Based Multi-Image Selection Strategy | Accepted |
| [DDR-015](./DDR-015-cli-directory-arguments.md) | 2025-12-31 | CLI Directory Arguments with Cobra | Accepted |
| [DDR-016](./DDR-016-quality-agnostic-photo-selection.md) | 2025-12-31 | Quality-Agnostic Metadata-Driven Photo Selection | Accepted |
| [DDR-017](./DDR-017-francis-reference-photo.md) | 2026-01-01 | Francis Reference Photo for Person Identification | Accepted |
| [DDR-018](./DDR-018-video-compression-gemini3.md) | 2026-01-01 | Video Compression for Gemini 3 Pro Optimization | Accepted |
| [DDR-019](./DDR-019-externalized-prompt-templates.md) | 2026-02-06 | Externalized Prompt Templates | Accepted |
| [DDR-020](./DDR-020-mixed-media-selection.md) | 2026-01-01 | Mixed Media Selection Strategy | Accepted |
| [DDR-021](./DDR-021-media-triage-command.md) | 2026-02-06 | Media Triage Command with Batch AI Evaluation | Accepted |
| [DDR-022](./DDR-022-web-ui-preact-spa.md) | 2026-02-06 | Web UI with Preact SPA and Go JSON API | Accepted |
| [DDR-023](./DDR-023-aws-iam-deployment-user.md) | 2026-02-06 | AWS IAM User and Scoped Policies for CDK Deployment | Accepted |
| [DDR-024](./DDR-024-full-image-preview-tooltip.md) | 2026-02-06 | Full-Image Preview and Filename Tooltip in Triage Web UI | Accepted |
| [DDR-025](./DDR-025-ssm-parameter-store-secrets.md) | 2026-02-06 | SSM Parameter Store for Runtime Secrets | Accepted |
| [DDR-026](./DDR-026-phase2-lambda-s3-deployment.md) | 2026-02-07 | Phase 2 Lambda + S3 Cloud Deployment | Accepted |
| [DDR-027](./DDR-027-container-image-lambda-local-commands.md) | 2026-02-07 | Container Image Lambda for Local OS Command Dependencies | Accepted |

---

## Status Legend

| Status | Meaning |
|--------|---------|
| **Accepted** | Decision is in effect |
| **Superseded** | Replaced by a newer decision |
| **Deprecated** | No longer applicable |

---

**Last Updated**: 2026-02-07 (DDR-027)

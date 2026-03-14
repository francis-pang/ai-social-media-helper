package fbprep

import "github.com/fpang/ai-social-media-helper/internal/batch"

// Re-export batch types for backward compatibility. Callers can use fbprep.BatchMeta
// or batch.BatchMeta interchangeably.
type BatchMeta = batch.BatchMeta
type MediaItem = batch.MediaItem
type SubmitDeps = batch.SubmitDeps
type GPS = batch.GPS

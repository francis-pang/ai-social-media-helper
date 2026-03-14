package batch

import (
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fpang/ai-social-media-helper/internal/store"
)

// SubmitDeps holds dependencies for building batch media parts.
type SubmitDeps struct {
	FileProcessStore *store.FileProcessingStore
	S3Client         *s3.Client
	PresignClient    *s3.PresignClient
	MediaBucket      string
}

// BatchMeta holds batch metadata for the submit step.
type BatchMeta struct {
	BatchIndex  int         `json:"batch_index"`
	MediaItems  []MediaItem `json:"media_items"`
	MetadataCtx string      `json:"metadata_ctx"`
	BaseIndex   int         `json:"base_index"`
	S3Keys      []string    `json:"s3_keys"`
}

// MediaItem represents a single media item in a batch.
type MediaItem struct {
	S3Key     string  `json:"s3_key"`
	MediaType string  `json:"media_type"`
	GPS       *GPS    `json:"gps,omitempty"`
	DateTaken string  `json:"date_taken,omitempty"`
	Filename  string  `json:"filename"`
}

// GPS holds latitude and longitude.
type GPS struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

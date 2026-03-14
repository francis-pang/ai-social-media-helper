package main

// FBPrepInput is the Lambda input.
type FBPrepInput struct {
	SessionID   string           `json:"session_id"`
	JobID       string           `json:"job_id,omitempty"`
	MediaItems  []FBPrepMediaItem `json:"media_items"`
	EconomyMode bool             `json:"economy_mode"`
}

// FBPrepMediaItem represents a single media item in the input.
type FBPrepMediaItem struct {
	S3Key     string  `json:"s3_key"`
	MediaType string  `json:"media_type"` // "image" or "video"
	GPS       *GPS    `json:"gps,omitempty"`
	DateTaken string  `json:"date_taken,omitempty"`
	Filename  string  `json:"filename"`
}

// GPS holds latitude and longitude.
type GPS struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// FBPrepOutput is the Lambda output.
type FBPrepOutput struct {
	SessionID        string            `json:"session_id"`
	Status           string            `json:"status"` // "complete" or "pending"
	BatchJobID       string            `json:"batch_job_id,omitempty"`
	BatchJobIDs      []string          `json:"batch_job_ids,omitempty"` // When multiple batches (>10 videos)
	JobID            string            `json:"job_id,omitempty"`
	VideosToUpload   []VideoToUpload   `json:"videos_to_upload,omitempty"` // For Map: one Lambda per video
	BatchesMeta      []FBPrepBatchMeta `json:"batches_meta,omitempty"`
	LocationTags     map[string]string `json:"location_tags,omitempty"`
	// Upload-video response:
	GsURI            string `json:"gs_uri,omitempty"`
	BatchIndex       int    `json:"batch_index,omitempty"`
	ItemIndexInBatch int    `json:"item_index_in_batch,omitempty"`
	S3Key            string `json:"s3_key,omitempty"`
}

// VideoToUpload is one video to upload to GCS (one Lambda invocation).
type VideoToUpload struct {
	S3Key            string `json:"s3_key"`
	UseKey           string `json:"use_key"` // S3 key of downscaled video
	JobID            string `json:"job_id"`
	BatchIndex       int    `json:"batch_index"`
	ItemIndexInBatch int    `json:"item_index_in_batch"`
}

// FBPrepBatchMeta holds batch metadata for submit step.
type FBPrepBatchMeta struct {
	BatchIndex  int               `json:"batch_index"`
	MediaItems  []FBPrepMediaItem `json:"media_items"`
	MetadataCtx string            `json:"metadata_ctx"`
	BaseIndex   int               `json:"base_index"`
	S3Keys      []string          `json:"s3_keys"`
}

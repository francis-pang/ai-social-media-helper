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
	SessionID  string `json:"session_id"`
	Status     string `json:"status"` // "complete" or "pending"
	BatchJobID string `json:"batch_job_id,omitempty"`
}

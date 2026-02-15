package main

// SelectionEvent is the input payload from Step Functions.
// It is produced by the state machine after the thumbnail Map state completes.
type SelectionEvent struct {
	SessionID     string           `json:"sessionId"`
	JobID         string           `json:"jobId"`
	TripContext   string           `json:"tripContext"`
	Model         string           `json:"model,omitempty"`
	MediaKeys     []string         `json:"mediaKeys"`
	ThumbnailKeys []ThumbnailEntry `json:"thumbnailKeys"`
	Bucket        string           `json:"bucket,omitempty"`
}

// ThumbnailEntry pairs an original media key with its generated thumbnail key.
type ThumbnailEntry struct {
	ThumbnailKey string `json:"thumbnailKey"`
	OriginalKey  string `json:"originalKey"`
}

// SelectionResult is the output returned to Step Functions.
type SelectionResult struct {
	JobID           string `json:"jobId"`
	SelectedCount   int    `json:"selectedCount"`
	ExcludedCount   int    `json:"excludedCount"`
	SceneGroupCount int    `json:"sceneGroupCount"`
	Error           string `json:"error,omitempty"`
}

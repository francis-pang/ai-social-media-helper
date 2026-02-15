package main

// EnhanceEvent is the input payload from Step Functions or async invocation.
// For Step Functions (initial enhancement): type is empty, key + itemIndex are set.
// For async feedback (DDR-053): type is "enhancement-feedback", key + feedback are set.
type EnhanceEvent struct {
	Type      string `json:"type,omitempty"`
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Key       string `json:"key"`
	ItemIndex int    `json:"itemIndex"`
	Bucket    string `json:"bucket,omitempty"`
	Feedback  string `json:"feedback,omitempty"` // DDR-053: enhancement feedback text
}

// EnhanceResult is the output returned to Step Functions.
type EnhanceResult struct {
	OriginalKey      string `json:"originalKey"`
	EnhancedKey      string `json:"enhancedKey"`
	EnhancedThumbKey string `json:"enhancedThumbKey"`
	Phase            string `json:"phase"`
	Phase1Text       string `json:"phase1Text,omitempty"`
	ImagenEdits      int    `json:"imagenEdits"`
	Error            string `json:"error,omitempty"`
}

package store

// FBPrepJob represents a Facebook post preparation job (DynamoDB SK = FBPREP#{jobId}).
// Each job processes a batch of media items and produces captions, location tags, and timestamps.
type FBPrepJob struct {
	ID          string       `json:"id" dynamodbav:"-"`
	SessionID   string       `json:"-" dynamodbav:"-"`
	Status      string       `json:"status" dynamodbav:"status"`
	EconomyMode bool         `json:"economyMode,omitempty" dynamodbav:"economyMode,omitempty"`
	MediaKeys   []string     `json:"mediaKeys,omitempty" dynamodbav:"mediaKeys,omitempty"`
	Items       []FBPrepItem `json:"items,omitempty" dynamodbav:"items,omitempty"`
	BatchJobID  string       `json:"batchJobId,omitempty"  dynamodbav:"batchJobId,omitempty"`
	InputTokens  int         `json:"inputTokens,omitempty"  dynamodbav:"inputTokens,omitempty"`
	OutputTokens int         `json:"outputTokens,omitempty" dynamodbav:"outputTokens,omitempty"`
	// PreEnrichLocations stores Maps-verified location tags resolved before batch submission
	// (DDR-085). Keys are string item indices ("0", "1", ...). Used to compare against
	// batch model output in handleCollectBatch to evaluate pre-enrichment accuracy.
	PreEnrichLocations map[string]string `json:"preEnrichLocations,omitempty" dynamodbav:"preEnrichLocations,omitempty"`
	CreatedAt   string       `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt   string       `json:"updatedAt" dynamodbav:"updatedAt"`
	Error       string       `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// FBPrepItem represents a single media item's Facebook prep output.
type FBPrepItem struct {
	ItemIndex          int    `json:"item_index" dynamodbav:"item_index"`
	S3Key              string `json:"s3_key" dynamodbav:"s3_key"`
	Key                string `json:"key,omitempty" dynamodbav:"key,omitempty"` // Alias for API compatibility
	Caption            string `json:"caption" dynamodbav:"caption"`
	LocationTag        string `json:"location_tag" dynamodbav:"location_tag"`
	DateTimestamp      string `json:"date_timestamp" dynamodbav:"date_timestamp"`
	LocationConfidence string `json:"location_confidence" dynamodbav:"location_confidence"`
}

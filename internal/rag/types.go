package rag

const (
	EventTriageFinalized      = "triage.finalized"
	EventSelectionFinalized   = "selection.finalized"
	EventOverrideAction       = "selection.override.action"
	EventOverridesFinalized   = "selection.overrides.finalized"
	EventDescriptionFinalized = "description.finalized"
	EventPublishFinalized     = "publish.finalized"
)

const (
	TableTriageDecisions    = "triage_decisions"
	TableSelectionDecisions = "selection_decisions"
	TableOverrideDecisions  = "override_decisions"
	TableCaptionDecisions   = "caption_decisions"
	TablePublishDecisions   = "publish_decisions"
)

type ContentFeedback struct {
	EventType     string            `json:"eventType"`
	SessionID     string            `json:"sessionId"`
	JobID         string            `json:"jobId"`
	Timestamp     string            `json:"timestamp"`
	UserID        string            `json:"userId"`
	MediaKey      string            `json:"mediaKey"`
	MediaType     string            `json:"mediaType"`
	AIVerdict     string            `json:"aiVerdict"`
	UserVerdict   string            `json:"userVerdict"`
	IsOverride    bool              `json:"isOverride"`
	Reason        string            `json:"reason"`
	Model         string            `json:"model"`
	PromptVersion string            `json:"promptVersion"`
	Metadata      map[string]string  `json:"metadata"`
}

type TriageDecision struct {
	ID           string            `json:"id"`
	SessionID    string            `json:"session_id"`
	UserID       string            `json:"user_id"`
	MediaKey     string            `json:"media_key"`
	Filename     string             `json:"filename"`
	MediaType    string             `json:"media_type"`
	Saveable     bool               `json:"saveable"`
	Reason       string             `json:"reason"`
	MediaMetadata map[string]string `json:"media_metadata"`
	Embedding    []float32          `json:"embedding"`
	CreatedAt    string             `json:"created_at"`
}

type SelectionDecision struct {
	ID                string            `json:"id"`
	SessionID         string            `json:"session_id"`
	UserID            string            `json:"user_id"`
	MediaKey          string            `json:"media_key"`
	Filename          string            `json:"filename"`
	MediaType         string            `json:"media_type"`
	Selected          bool              `json:"selected"`
	ExclusionCategory string            `json:"exclusion_category"`
	ExclusionReason   string            `json:"exclusion_reason"`
	SceneGroup        string            `json:"scene_group"`
	MediaMetadata     map[string]string `json:"media_metadata"`
	Embedding         []float32         `json:"embedding"`
	CreatedAt         string            `json:"created_at"`
}

type OverrideDecision struct {
	ID           string            `json:"id"`
	SessionID    string            `json:"session_id"`
	UserID       string            `json:"user_id"`
	MediaKey     string            `json:"media_key"`
	Filename     string            `json:"filename"`
	MediaType    string            `json:"media_type"`
	Action       string            `json:"action"`
	AIVerdict    string            `json:"ai_verdict"`
	AIReason     string            `json:"ai_reason"`
	IsFinalized  bool              `json:"is_finalized"`
	MediaMetadata map[string]string `json:"media_metadata"`
	Embedding    []float32         `json:"embedding"`
	CreatedAt    string            `json:"created_at"`
}

type CaptionDecision struct {
	ID            string            `json:"id"`
	SessionID     string            `json:"session_id"`
	UserID        string            `json:"user_id"`
	CaptionText   string            `json:"caption_text"`
	Hashtags      []string          `json:"hashtags"`
	LocationTag   string            `json:"location_tag"`
	MediaKeys     []string          `json:"media_keys"`
	PostGroupName string            `json:"post_group_name"`
	MediaMetadata map[string]string `json:"media_metadata"`
	Embedding     []float32         `json:"embedding"`
	CreatedAt     string            `json:"created_at"`
}

type PublishDecision struct {
	ID            string            `json:"id"`
	SessionID     string            `json:"session_id"`
	UserID        string            `json:"user_id"`
	Platform      string            `json:"platform"`
	PostGroupName string            `json:"post_group_name"`
	CaptionText   string            `json:"caption_text"`
	Hashtags      []string          `json:"hashtags"`
	LocationTag   string            `json:"location_tag"`
	MediaKeys     []string          `json:"media_keys"`
	MediaMetadata map[string]string `json:"media_metadata"`
	Embedding     []float32         `json:"embedding"`
	CreatedAt     string            `json:"created_at"`
}

type PreferenceProfile struct {
	PK                 string            `json:"pk"`
	SK                 string            `json:"sk"`
	UserID             string            `json:"user_id"`
	ProfileText        string            `json:"profile_text"`
	CaptionExamplesText string           `json:"caption_examples_text"`
	Stats              map[string]int    `json:"stats"`
	ComputedAt         string           `json:"computed_at"`
	Version            int               `json:"version"`
}

type QueryRequest struct {
	QueryType      string            `json:"queryType"`
	UserID         string            `json:"userId"`
	SessionContext string            `json:"sessionContext"`
	MediaMetadata  map[string]string `json:"mediaMetadata"`
}

type QueryResponse struct {
	RAGContext string `json:"ragContext"`
	Source    string `json:"source"`
}

// Package store provides persistent session state storage for the
// multi-step media selection workflow. It replaces the in-memory job
// maps in the Lambda handler with DynamoDB-backed storage that survives
// Lambda container recycling, concurrent invocations, and deployments.
//
// The package uses a single-table DynamoDB design where all records for
// a session share a partition key (SESSION#{sessionId}). Sort keys
// distinguish record types: META, SELECTION#, ENHANCE#, DOWNLOAD#,
// DESC#, and GROUP#. A TTL attribute (expiresAt) auto-deletes records
// after 24 hours, matching the S3 media lifecycle policy.
//
// See DDR-039: DynamoDB SessionStore for Persistent Multi-Step State.
package store

import (
	"context"
	"time"
)

// SessionTTL is the default time-to-live for all DynamoDB records.
// Matches the S3 media bucket lifecycle policy (24 hours).
const SessionTTL = 24 * time.Hour

// StepOrder defines the cascade order for downstream invalidation.
// When a user navigates back to step N and re-triggers processing,
// all state for steps N through the end is invalidated.
var StepOrder = []string{"triage", "selection", "enhancement", "grouping", "download", "description", "publish"}

// SessionStore defines the persistence interface for multi-step workflow state.
// Each method is safe for concurrent use. Implementations must handle
// context cancellation and propagate errors with sufficient detail for debugging.
//
// All Get methods return (nil, nil) when the requested record does not exist.
// All Put methods perform full-item replacement (upsert semantics).
type SessionStore interface {
	// --- Session metadata ---

	// PutSession creates or replaces a session metadata record.
	PutSession(ctx context.Context, session *Session) error

	// GetSession retrieves session metadata by ID. Returns nil, nil if not found.
	GetSession(ctx context.Context, sessionID string) (*Session, error)

	// UpdateSessionStatus atomically updates the status field of a session
	// without overwriting other fields. Uses DynamoDB UpdateItem.
	UpdateSessionStatus(ctx context.Context, sessionID, status string) error

	// --- Triage jobs (DDR-050) ---

	// PutTriageJob creates or replaces a triage job record.
	PutTriageJob(ctx context.Context, sessionID string, job *TriageJob) error

	// GetTriageJob retrieves a triage job. Returns nil, nil if not found.
	GetTriageJob(ctx context.Context, sessionID, jobID string) (*TriageJob, error)

	// --- Selection jobs ---

	// PutSelectionJob creates or replaces a selection job record.
	PutSelectionJob(ctx context.Context, sessionID string, job *SelectionJob) error

	// GetSelectionJob retrieves a selection job. Returns nil, nil if not found.
	GetSelectionJob(ctx context.Context, sessionID, jobID string) (*SelectionJob, error)

	// --- Enhancement jobs ---

	// PutEnhancementJob creates or replaces an enhancement job record.
	PutEnhancementJob(ctx context.Context, sessionID string, job *EnhancementJob) error

	// GetEnhancementJob retrieves an enhancement job. Returns nil, nil if not found.
	GetEnhancementJob(ctx context.Context, sessionID, jobID string) (*EnhancementJob, error)

	// --- Download jobs ---

	// PutDownloadJob creates or replaces a download job record.
	PutDownloadJob(ctx context.Context, sessionID string, job *DownloadJob) error

	// GetDownloadJob retrieves a download job. Returns nil, nil if not found.
	GetDownloadJob(ctx context.Context, sessionID, jobID string) (*DownloadJob, error)

	// --- Description jobs ---

	// PutDescriptionJob creates or replaces a description job record.
	PutDescriptionJob(ctx context.Context, sessionID string, job *DescriptionJob) error

	// GetDescriptionJob retrieves a description job. Returns nil, nil if not found.
	GetDescriptionJob(ctx context.Context, sessionID, jobID string) (*DescriptionJob, error)

	// --- Post groups ---

	// PutPostGroup creates or replaces a post group record.
	PutPostGroup(ctx context.Context, sessionID string, group *PostGroup) error

	// GetPostGroups retrieves all post groups for a session.
	GetPostGroups(ctx context.Context, sessionID string) ([]*PostGroup, error)

	// DeletePostGroup deletes a single post group.
	DeletePostGroup(ctx context.Context, sessionID, groupID string) error

	// --- Publish jobs ---

	// PutPublishJob creates or replaces a publish job record.
	PutPublishJob(ctx context.Context, sessionID string, job *PublishJob) error

	// GetPublishJob retrieves a publish job. Returns nil, nil if not found.
	GetPublishJob(ctx context.Context, sessionID, jobID string) (*PublishJob, error)

	// --- Session invalidation ---

	// InvalidateDownstream deletes all job records for steps at or after fromStep.
	// Valid step names: "selection", "enhancement", "grouping", "download", "description", "publish".
	// Returns the list of deleted sort key values for logging.
	InvalidateDownstream(ctx context.Context, sessionID, fromStep string) ([]string, error)
}

// --- Domain types ---
//
// Each type maps to a DynamoDB record. The ID and SessionID fields are
// derived from PK/SK on read and excluded from DynamoDB attributes on write
// (via dynamodbav:"-"). All other fields are stored as DynamoDB attributes.

// Session represents session metadata (DynamoDB SK = META).
type Session struct {
	ID           string   `json:"id" dynamodbav:"-"`
	Status       string   `json:"status" dynamodbav:"status"`
	TripContext  string   `json:"tripContext,omitempty" dynamodbav:"tripContext,omitempty"`
	UploadedKeys []string `json:"uploadedKeys,omitempty" dynamodbav:"uploadedKeys,omitempty"`
	CreatedAt    int64    `json:"createdAt" dynamodbav:"createdAt"`
}

// TriageJob represents AI triage results (DynamoDB SK = TRIAGE#{jobId}).
// Added by DDR-050 to support async triage via Worker Lambda.
type TriageJob struct {
	ID            string       `json:"id" dynamodbav:"-"`
	SessionID     string       `json:"-" dynamodbav:"-"`
	Status        string       `json:"status" dynamodbav:"status"`
	Phase         string       `json:"phase,omitempty" dynamodbav:"phase,omitempty"`
	TotalFiles    int          `json:"totalFiles,omitempty" dynamodbav:"totalFiles,omitempty"`
	UploadedFiles int          `json:"uploadedFiles,omitempty" dynamodbav:"uploadedFiles,omitempty"`
	Keep          []TriageItem `json:"keep,omitempty" dynamodbav:"keep,omitempty"`
	Discard       []TriageItem `json:"discard,omitempty" dynamodbav:"discard,omitempty"`
	Error         string       `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// TriageItem represents a single media item in triage results.
type TriageItem struct {
	Media        int    `json:"media" dynamodbav:"media"`
	Filename     string `json:"filename" dynamodbav:"filename"`
	Key          string `json:"key" dynamodbav:"key"`
	Saveable     bool   `json:"saveable" dynamodbav:"saveable"`
	Reason       string `json:"reason" dynamodbav:"reason"`
	ThumbnailURL string `json:"thumbnailUrl" dynamodbav:"thumbnailUrl"`
}

// SelectionJob represents AI selection results (DynamoDB SK = SELECTION#{jobId}).
type SelectionJob struct {
	ID          string         `json:"id" dynamodbav:"-"`
	SessionID   string         `json:"-" dynamodbav:"-"`
	Status      string         `json:"status" dynamodbav:"status"`
	Selected    []SelectedItem `json:"selected,omitempty" dynamodbav:"selected,omitempty"`
	Excluded    []ExcludedItem `json:"excluded,omitempty" dynamodbav:"excluded,omitempty"`
	SceneGroups []SceneGroup   `json:"sceneGroups,omitempty" dynamodbav:"sceneGroups,omitempty"`
	Error       string         `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// SelectedItem represents a media item chosen by the AI.
type SelectedItem struct {
	Rank           int    `json:"rank" dynamodbav:"rank"`
	Media          int    `json:"media" dynamodbav:"media"`
	Filename       string `json:"filename" dynamodbav:"filename"`
	Key            string `json:"key" dynamodbav:"key"`
	Type           string `json:"type" dynamodbav:"type"`
	Scene          string `json:"scene" dynamodbav:"scene"`
	Justification  string `json:"justification" dynamodbav:"justification"`
	ComparisonNote string `json:"comparisonNote,omitempty" dynamodbav:"comparisonNote,omitempty"`
	ThumbnailURL   string `json:"thumbnailUrl" dynamodbav:"thumbnailUrl"`
}

// ExcludedItem represents a media item not chosen by the AI.
type ExcludedItem struct {
	Media        int    `json:"media" dynamodbav:"media"`
	Filename     string `json:"filename" dynamodbav:"filename"`
	Key          string `json:"key" dynamodbav:"key"`
	Reason       string `json:"reason" dynamodbav:"reason"`
	Category     string `json:"category" dynamodbav:"category"`
	DuplicateOf  string `json:"duplicateOf,omitempty" dynamodbav:"duplicateOf,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl" dynamodbav:"thumbnailUrl"`
}

// SceneGroup is a group of media items belonging to the same scene.
type SceneGroup struct {
	Name      string           `json:"name" dynamodbav:"name"`
	GPS       string           `json:"gps,omitempty" dynamodbav:"gps,omitempty"`
	TimeRange string           `json:"timeRange,omitempty" dynamodbav:"timeRange,omitempty"`
	Items     []SceneGroupItem `json:"items" dynamodbav:"items"`
}

// SceneGroupItem is a media item within a scene group.
type SceneGroupItem struct {
	Media        int    `json:"media" dynamodbav:"media"`
	Filename     string `json:"filename" dynamodbav:"filename"`
	Key          string `json:"key" dynamodbav:"key"`
	Type         string `json:"type" dynamodbav:"type"`
	Selected     bool   `json:"selected" dynamodbav:"selected"`
	Description  string `json:"description" dynamodbav:"description"`
	ThumbnailURL string `json:"thumbnailUrl" dynamodbav:"thumbnailUrl"`
}

// EnhancementJob represents a photo enhancement pipeline run
// (DynamoDB SK = ENHANCE#{jobId}).
type EnhancementJob struct {
	ID             string            `json:"id" dynamodbav:"-"`
	SessionID      string            `json:"-" dynamodbav:"-"`
	Status         string            `json:"status" dynamodbav:"status"`
	Items          []EnhancementItem `json:"items,omitempty" dynamodbav:"items,omitempty"`
	TotalCount     int               `json:"totalCount" dynamodbav:"totalCount"`
	CompletedCount int               `json:"completedCount" dynamodbav:"completedCount"`
	Error          string            `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// EnhancementItem tracks enhancement state for a single photo.
type EnhancementItem struct {
	Key              string          `json:"key" dynamodbav:"key"`
	Filename         string          `json:"filename" dynamodbav:"filename"`
	Phase            string          `json:"phase" dynamodbav:"phase"`
	OriginalKey      string          `json:"originalKey" dynamodbav:"originalKey"`
	EnhancedKey      string          `json:"enhancedKey,omitempty" dynamodbav:"enhancedKey,omitempty"`
	OriginalThumbKey string          `json:"originalThumbKey,omitempty" dynamodbav:"originalThumbKey,omitempty"`
	EnhancedThumbKey string          `json:"enhancedThumbKey,omitempty" dynamodbav:"enhancedThumbKey,omitempty"`
	Phase1Text       string          `json:"phase1Text,omitempty" dynamodbav:"phase1Text,omitempty"`
	Analysis         *AnalysisResult `json:"analysis,omitempty" dynamodbav:"analysis,omitempty"`
	ImagenEdits      int             `json:"imagenEdits" dynamodbav:"imagenEdits"`
	FeedbackHistory  []FeedbackEntry `json:"feedbackHistory,omitempty" dynamodbav:"feedbackHistory,omitempty"`
	Error            string          `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// AnalysisResult is the Phase 2 quality analysis output.
// Mirrors chat.AnalysisResult for DynamoDB persistence.
type AnalysisResult struct {
	OverallAssessment     string            `json:"overallAssessment" dynamodbav:"overallAssessment"`
	RemainingImprovements []ImprovementItem `json:"remainingImprovements,omitempty" dynamodbav:"remainingImprovements,omitempty"`
	ProfessionalScore     float64           `json:"professionalScore" dynamodbav:"professionalScore"`
	TargetScore           float64           `json:"targetScore" dynamodbav:"targetScore"`
	NoFurtherEditsNeeded  bool              `json:"noFurtherEditsNeeded" dynamodbav:"noFurtherEditsNeeded"`
}

// ImprovementItem describes a remaining enhancement opportunity.
type ImprovementItem struct {
	Type            string `json:"type" dynamodbav:"type"`
	Description     string `json:"description" dynamodbav:"description"`
	Region          string `json:"region" dynamodbav:"region"`
	Impact          string `json:"impact" dynamodbav:"impact"`
	ImagenSuitable  bool   `json:"imagenSuitable" dynamodbav:"imagenSuitable"`
	EditInstruction string `json:"editInstruction" dynamodbav:"editInstruction"`
}

// FeedbackEntry records one round of enhancement feedback and its result.
type FeedbackEntry struct {
	UserFeedback  string `json:"userFeedback" dynamodbav:"userFeedback"`
	ModelResponse string `json:"modelResponse" dynamodbav:"modelResponse"`
	Method        string `json:"method" dynamodbav:"method"`
	Success       bool   `json:"success" dynamodbav:"success"`
}

// DownloadJob represents a ZIP bundle creation job
// (DynamoDB SK = DOWNLOAD#{jobId}).
type DownloadJob struct {
	ID        string           `json:"id" dynamodbav:"-"`
	SessionID string           `json:"-" dynamodbav:"-"`
	Status    string           `json:"status" dynamodbav:"status"`
	Bundles   []DownloadBundle `json:"bundles,omitempty" dynamodbav:"bundles,omitempty"`
	Error     string           `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// DownloadBundle represents a single ZIP archive in a download job.
type DownloadBundle struct {
	Type        string `json:"type" dynamodbav:"type"`
	Name        string `json:"name" dynamodbav:"name"`
	ZipKey      string `json:"zipKey,omitempty" dynamodbav:"zipKey,omitempty"`
	DownloadURL string `json:"downloadUrl,omitempty" dynamodbav:"downloadUrl,omitempty"`
	FileCount   int    `json:"fileCount" dynamodbav:"fileCount"`
	TotalSize   int64  `json:"totalSize" dynamodbav:"totalSize"`
	ZipSize     int64  `json:"zipSize,omitempty" dynamodbav:"zipSize,omitempty"`
	Status      string `json:"status" dynamodbav:"bundleStatus"`
	Error       string `json:"error,omitempty" dynamodbav:"bundleError,omitempty"`
}

// DescriptionJob represents an AI caption generation job
// (DynamoDB SK = DESC#{jobId}).
type DescriptionJob struct {
	ID          string              `json:"id" dynamodbav:"-"`
	SessionID   string              `json:"-" dynamodbav:"-"`
	Status      string              `json:"status" dynamodbav:"status"`
	GroupLabel  string              `json:"groupLabel,omitempty" dynamodbav:"groupLabel,omitempty"`
	TripContext string              `json:"tripContext,omitempty" dynamodbav:"tripContext,omitempty"`
	MediaKeys   []string            `json:"mediaKeys,omitempty" dynamodbav:"mediaKeys,omitempty"`
	Caption     string              `json:"caption,omitempty" dynamodbav:"caption,omitempty"`
	Hashtags    []string            `json:"hashtags,omitempty" dynamodbav:"hashtags,omitempty"`
	LocationTag string              `json:"locationTag,omitempty" dynamodbav:"locationTag,omitempty"`
	RawResponse string              `json:"-" dynamodbav:"rawResponse,omitempty"`
	History     []ConversationEntry `json:"history,omitempty" dynamodbav:"history,omitempty"`
	Error       string              `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// ConversationEntry records one round of description feedback.
type ConversationEntry struct {
	UserFeedback  string `json:"userFeedback" dynamodbav:"userFeedback"`
	ModelResponse string `json:"modelResponse" dynamodbav:"modelResponse"`
}

// PublishJob represents an Instagram publishing job (DynamoDB SK = PUBLISH#{jobId}).
type PublishJob struct {
	ID              string   `json:"id" dynamodbav:"-"`
	SessionID       string   `json:"-" dynamodbav:"-"`
	GroupID         string   `json:"groupId" dynamodbav:"groupId"`
	Status          string   `json:"status" dynamodbav:"status"`
	Phase           string   `json:"phase" dynamodbav:"phase"`
	TotalItems      int      `json:"totalItems" dynamodbav:"totalItems"`
	CompletedItems  int      `json:"completedItems" dynamodbav:"completedItems"`
	InstagramPostID string   `json:"instagramPostId,omitempty" dynamodbav:"instagramPostId,omitempty"`
	ContainerIDs    []string `json:"containerIds,omitempty" dynamodbav:"containerIds,omitempty"`
	Error           string   `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// PostGroup represents a user-created post group (DynamoDB SK = GROUP#{groupId}).
// Each group is one Instagram carousel or one download bundle.
type PostGroup struct {
	ID              string   `json:"id" dynamodbav:"-"`
	Name            string   `json:"name,omitempty" dynamodbav:"name,omitempty"`
	MediaKeys       []string `json:"mediaKeys,omitempty" dynamodbav:"mediaKeys,omitempty"`
	Caption         string   `json:"caption,omitempty" dynamodbav:"caption,omitempty"`
	PublishStatus   string   `json:"publishStatus,omitempty" dynamodbav:"publishStatus,omitempty"`
	InstagramPostID string   `json:"instagramPostId,omitempty" dynamodbav:"instagramPostId,omitempty"`
}

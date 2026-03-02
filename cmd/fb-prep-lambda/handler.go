package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"

	"github.com/fpang/ai-social-media-helper/internal/assets"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/media"
	"github.com/fpang/ai-social-media-helper/internal/s3util"
	"github.com/fpang/ai-social-media-helper/internal/store"
)

// fbPrepResponseItem matches the JSON output format from the AI.
type fbPrepResponseItem struct {
	ItemIndex          int    `json:"item_index"`
	Caption            string `json:"caption"`
	LocationTag        string `json:"location_tag"`
	DateTimestamp      string `json:"date_timestamp"`
	LocationConfidence string `json:"location_confidence"`
}

func handler(ctx context.Context, event interface{}) (out *FBPrepOutput, retErr error) {
	// Check for special event types before attempting batch normalization.
	if m, ok := event.(map[string]interface{}); ok {
		if t, _ := m["type"].(string); t == "fb-prep-feedback" {
			return handleFeedback(ctx, m)
		}
		if t, _ := m["type"].(string); t == "fb-prep-collect-batch" {
			return handleCollectBatch(ctx, m)
		}
	}

	input, err := normalizeFBPrepInput(event)
	if err != nil {
		return nil, err
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if len(input.MediaItems) == 0 {
		return nil, fmt.Errorf("media_items cannot be empty")
	}

	genaiClient, err := ai.NewAIClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize AI client: %w", err)
	}

	// Always update DynamoDB to "error" if we return a non-nil error.
	// Covers both the initial invocation and AWS async retries (DDR-081).
	defer func() {
		if retErr != nil && sessionStore != nil && input != nil {
			now := time.Now().UTC().Format(time.RFC3339)
			_ = sessionStore.PutFBPrepJob(ctx, input.SessionID, &store.FBPrepJob{
				ID:        input.JobID,
				Status:    "error",
				Error:     retErr.Error(),
				UpdatedAt: now,
			})
		}
	}()

	// Economy mode: submit to Gemini Batch API (FBPrepPipeline SFN polls to completion, DDR-082).
	if input.EconomyMode {
		parts, metadataCtx, s3Keys, err := buildFBPrepMediaParts(ctx, input.MediaItems)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare media: %w", err)
		}
		prompt := buildFBPrepPrompt(metadataCtx)
		parts = append(parts, &genai.Part{Text: prompt})
		config := &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: assets.FBPrepSystemPrompt}},
			},
			Tools: []*genai.Tool{{GoogleMaps: &genai.GoogleMaps{}}},
		}
		modelName := ai.GetModelName()
		now := time.Now().UTC().Format(time.RFC3339)
		jobID := input.JobID
		if jobID == "" {
			jobID = "fbprep-" + uuid.New().String()[:8]
		}
		req := &genai.InlinedRequest{
			Model:    modelName,
			Contents: []*genai.Content{{Role: "user", Parts: parts}},
			Config:   config,
		}
		batchJobID, err := ai.SubmitGeminiBatch(ctx, genaiClient, modelName, []*genai.InlinedRequest{req})
		if err != nil {
			return nil, fmt.Errorf("failed to submit batch job: %w", err)
		}
		if sessionStore != nil {
			_ = sessionStore.PutFBPrepJob(ctx, input.SessionID, &store.FBPrepJob{
				ID:          jobID,
				Status:      "pending",
				BatchJobID:  batchJobID,
				MediaKeys:   s3Keys,
				EconomyMode: true,
				CreatedAt:   now,
				UpdatedAt:   now,
			})
		}
		log.Info().
			Str("sessionId", input.SessionID).
			Str("batchJobId", batchJobID).
			Int("mediaCount", len(input.MediaItems)).
			Msg("FB prep batch job submitted (economy mode)")
		return &FBPrepOutput{
			SessionID:  input.SessionID,
			Status:     "pending",
			BatchJobID: batchJobID,
		}, nil
	}

	// Build media parts and metadata context
	parts, metadataCtx, s3Keys, err := buildFBPrepMediaParts(ctx, input.MediaItems)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare media: %w", err)
	}

	// Append metadata context as text
	prompt := buildFBPrepPrompt(metadataCtx)
	parts = append(parts, &genai.Part{Text: prompt})

	// Config with system instruction and Google Maps grounding
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: assets.FBPrepSystemPrompt}},
		},
		Tools: []*genai.Tool{{GoogleMaps: &genai.GoogleMaps{}}},
	}

	modelName := ai.GetModelName()
	now := time.Now().UTC().Format(time.RFC3339)
	jobID := input.JobID
	if jobID == "" {
		jobID = "fbprep-" + uuid.New().String()[:8]
	}

	// Real-time generation (economy mode removed — FB Prep has no SFN poller, DDR-081)
	contents := []*genai.Content{{Role: "user", Parts: parts}}
	resp, err := genaiClient.Models.GenerateContent(ctx, modelName, contents, config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	responseText := resp.Text()
	if responseText == "" {
		return nil, fmt.Errorf("received empty response from Gemini")
	}

	var inputTokens, outputTokens int
	if resp != nil && resp.UsageMetadata != nil {
		inputTokens = int(resp.UsageMetadata.PromptTokenCount)
		outputTokens = int(resp.UsageMetadata.CandidatesTokenCount)
	}

	items, err := parseFBPrepResponse(responseText, s3Keys)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Store complete job
	if sessionStore != nil {
		_ = sessionStore.PutFBPrepJob(ctx, input.SessionID, &store.FBPrepJob{
			ID:           jobID,
			Status:       "complete",
			Items:        items,
			MediaKeys:    s3Keys,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}

	log.Info().
		Str("sessionId", input.SessionID).
		Int("itemCount", len(items)).
		Msg("FB prep complete")

	return &FBPrepOutput{
		SessionID: input.SessionID,
		Status:    "complete",
	}, nil
}

func buildFBPrepMediaParts(ctx context.Context, mediaItems []FBPrepMediaItem) ([]*genai.Part, string, []string, error) {
	var parts []*genai.Part
	var metaLines []string
	var s3Keys []string

	for i, item := range mediaItems {
		s3Keys = append(s3Keys, item.S3Key)
		ext := strings.ToLower(filepath.Ext(item.S3Key))

		if item.MediaType == "image" || media.IsImage(ext) {
			// Try pre-generated thumbnail first; fall back to download + generate.
			keyParts := strings.SplitN(item.S3Key, "/", 2)
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", keyParts[0], strings.TrimSuffix(filepath.Base(item.S3Key), filepath.Ext(item.S3Key)))

			tmpPath, cleanup, err := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, thumbKey)
			if err != nil {
				// Fallback: download original and generate thumbnail in-memory.
				origPath, origCleanup, origErr := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, item.S3Key)
				if origErr != nil {
					log.Warn().Str("key", item.S3Key).Err(origErr).Msg("Skipping: failed to download")
					continue
				}
				origData, readErr := os.ReadFile(origPath)
				origCleanup() // free immediately after reading
				if readErr != nil {
					log.Warn().Str("key", item.S3Key).Err(readErr).Msg("Skipping: failed to read original")
					continue
				}
				mime := "image/jpeg"
				if m, ok := media.SupportedImageExtensions[ext]; ok {
					mime = m
				}
				thumbData, thumbMIME, thumbErr := s3util.GenerateThumbnailFromBytes(origData, mime, media.DefaultThumbnailMaxDimension)
				if thumbErr != nil {
					log.Warn().Str("key", item.S3Key).Err(thumbErr).Msg("Skipping: failed to generate thumbnail")
					continue
				}
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{MIMEType: thumbMIME, Data: thumbData},
				})
			} else {
				data, err := os.ReadFile(tmpPath)
				cleanup() // free immediately after reading
				if err != nil {
					log.Warn().Str("key", item.S3Key).Err(err).Msg("Skipping: failed to read")
					continue
				}
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: data},
				})
			}
		} else if item.MediaType == "video" || media.IsVideo(ext) {
			tmpPath, cleanup, err := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, item.S3Key)
			if err != nil {
				// Video download failed — fall back to thumbnail image.
				keyParts := strings.SplitN(item.S3Key, "/", 2)
				thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", keyParts[0], strings.TrimSuffix(filepath.Base(item.S3Key), filepath.Ext(item.S3Key)))
				thumbPath, thumbCleanup, thumbErr := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, thumbKey)
				if thumbErr != nil {
					log.Warn().Str("key", item.S3Key).Err(thumbErr).Msg("Skipping: failed to download video/thumbnail")
					continue
				}
				data, _ := os.ReadFile(thumbPath)
				thumbCleanup() // free immediately after reading
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{MIMEType: "image/jpeg", Data: data},
				})
			} else {
				var videoMeta *media.VideoMetadata
				if item.GPS != nil {
					videoMeta = &media.VideoMetadata{
						Latitude: item.GPS.Latitude, Longitude: item.GPS.Longitude, HasGPS: true,
					}
				}
				// CompressVideoForCaptions reads tmpPath, so cleanup() must wait until after compression.
				compressedPath, _, compCleanup, compErr := media.CompressVideoForCaptions(ctx, tmpPath, videoMeta)
				cleanup() // original download no longer needed after compression
				if compErr != nil {
					log.Warn().Str("key", item.S3Key).Err(compErr).Msg("Skipping: video compression failed")
					continue
				}
				data, readErr := os.ReadFile(compressedPath)
				compCleanup() // free compressed file immediately after reading
				if readErr != nil {
					log.Warn().Str("key", item.S3Key).Err(readErr).Msg("Skipping: failed to read compressed video")
					continue
				}
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{MIMEType: "video/webm", Data: data},
				})
			}
		}

		// Metadata line for this item
		metaLines = append(metaLines, fmt.Sprintf("Item %d (%s):", i, item.Filename))
		if item.GPS != nil {
			metaLines = append(metaLines, fmt.Sprintf("  GPS: %.6f, %.6f", item.GPS.Latitude, item.GPS.Longitude))
		}
		if item.DateTaken != "" {
			metaLines = append(metaLines, fmt.Sprintf("  Date: %s", item.DateTaken))
		}
	}

	metadataCtx := strings.Join(metaLines, "\n")
	return parts, metadataCtx, s3Keys, nil
}

func buildFBPrepPrompt(metadataCtx string) string {
	return "## Metadata context\n\n" + metadataCtx + "\n\nGenerate the JSON array for each item in the same order as above."
}

// normalizeFBPrepInput accepts either FBPrepInput or API event format (sessionId, jobId, mediaKeys, economyMode).
func normalizeFBPrepInput(event interface{}) (*FBPrepInput, error) {
	// Try API format first (map with sessionId, jobId, mediaKeys)
	if m, ok := event.(map[string]interface{}); ok {
		sessionID, _ := m["sessionId"].(string)
		jobID, _ := m["jobId"].(string)
		economyMode, _ := m["economyMode"].(bool)
		keysRaw, ok := m["mediaKeys"].([]interface{})
		if !ok {
			return nil, fmt.Errorf("mediaKeys is required")
		}
		mediaItems := make([]FBPrepMediaItem, 0, len(keysRaw))
		for _, k := range keysRaw {
			key, _ := k.(string)
			if key == "" {
				continue
			}
			mediaType := "image"
			if media.IsVideo(strings.ToLower(filepath.Ext(key))) {
				mediaType = "video"
			}
			mediaItems = append(mediaItems, FBPrepMediaItem{
				S3Key:     key,
				MediaType: mediaType,
				Filename:  filepath.Base(key),
			})
		}
		return &FBPrepInput{
			SessionID:   sessionID,
			JobID:       jobID,
			MediaItems:  mediaItems,
			EconomyMode: economyMode,
		}, nil
	}

	// Try direct FBPrepInput (JSON)
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	var input FBPrepInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	return &input, nil
}

func parseFBPrepResponse(responseText string, s3Keys []string) ([]store.FBPrepItem, error) {
	// Strip markdown code fences if present
	text := strings.TrimSpace(responseText)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		var filtered []string
		for _, line := range lines {
			if line == "```" || line == "```json" {
				continue
			}
			filtered = append(filtered, line)
		}
		text = strings.Join(filtered, "\n")
	}

	var raw []fbPrepResponseItem
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	items := make([]store.FBPrepItem, 0, len(raw))
	for _, r := range raw {
		s3Key := ""
		if r.ItemIndex >= 0 && r.ItemIndex < len(s3Keys) {
			s3Key = s3Keys[r.ItemIndex]
		}
		items = append(items, store.FBPrepItem{
			ItemIndex:          r.ItemIndex,
			S3Key:              s3Key,
			Key:                s3Key,
			Caption:            r.Caption,
			LocationTag:        r.LocationTag,
			DateTimestamp:      r.DateTimestamp,
			LocationConfidence: r.LocationConfidence,
		})
	}
	return items, nil
}

// handleFeedback regenerates the caption for a single item using user feedback (DDR-078 §4).
// It loads the existing job, re-runs Gemini for just the target item with sibling captions as context,
// and updates only that item in DynamoDB.
func handleFeedback(ctx context.Context, m map[string]interface{}) (*FBPrepOutput, error) {
	sessionID, _ := m["sessionId"].(string)
	jobID, _ := m["jobId"].(string)
	feedbackText, _ := m["feedback"].(string)

	itemIndexRaw, _ := m["itemIndex"].(float64)
	itemIndex := int(itemIndexRaw)

	if sessionID == "" || jobID == "" || feedbackText == "" {
		return nil, fmt.Errorf("feedback: sessionId, jobId, and feedback are required")
	}

	if sessionStore == nil {
		return nil, fmt.Errorf("feedback: session store not configured")
	}

	// Load existing job to get the media key and sibling captions.
	job, err := sessionStore.GetFBPrepJob(ctx, sessionID, jobID)
	if err != nil || job == nil {
		return nil, fmt.Errorf("feedback: job not found: %w", err)
	}
	if itemIndex < 0 || itemIndex >= len(job.Items) {
		return nil, fmt.Errorf("feedback: item index %d out of range (job has %d items)", itemIndex, len(job.Items))
	}

	targetItem := job.Items[itemIndex]
	s3Key := targetItem.S3Key
	if s3Key == "" {
		s3Key = targetItem.Key
	}

	// Build media part for the target item only.
	targetMediaItem := FBPrepMediaItem{
		S3Key:     s3Key,
		MediaType: "image",
		Filename:  filepath.Base(s3Key),
	}
	ext := strings.ToLower(filepath.Ext(s3Key))
	if media.IsVideo(ext) {
		targetMediaItem.MediaType = "video"
	}

	parts, _, _, err := buildFBPrepMediaParts(ctx, []FBPrepMediaItem{targetMediaItem})
	if err != nil {
		return nil, fmt.Errorf("feedback: failed to prepare media: %w", err)
	}

	// Build context: include sibling captions (text only) for narrative coherence.
	var siblingLines []string
	for i, item := range job.Items {
		if i == itemIndex {
			siblingLines = append(siblingLines, fmt.Sprintf("Item %d (THIS ITEM — regenerate): filename=%s", i, filepath.Base(item.S3Key)))
			continue
		}
		siblingLines = append(siblingLines, fmt.Sprintf("Item %d (accepted): caption=%q, location=%q", i, item.Caption, item.LocationTag))
	}

	prompt := fmt.Sprintf(
		"## Existing session captions (for narrative context)\n\n%s\n\n## Feedback for item %d\n\n%s\n\n"+
			"Regenerate only item %d's caption, location, and date. Return a JSON array with exactly one object.",
		strings.Join(siblingLines, "\n"), itemIndex, feedbackText, itemIndex,
	)
	parts = append(parts, &genai.Part{Text: prompt})

	genaiClient, err := ai.NewAIClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("feedback: failed to initialize AI client: %w", err)
	}

	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: assets.FBPrepSystemPrompt}},
		},
		Tools: []*genai.Tool{{GoogleMaps: &genai.GoogleMaps{}}},
	}

	modelName := ai.GetModelName()
	contents := []*genai.Content{{Role: "user", Parts: parts}}
	resp, err := genaiClient.Models.GenerateContent(ctx, modelName, contents, config)
	if err != nil {
		return nil, fmt.Errorf("feedback: Gemini call failed: %w", err)
	}

	responseText := resp.Text()
	if responseText == "" {
		return nil, fmt.Errorf("feedback: empty response from Gemini")
	}

	// Parse the single-item response.
	newItems, err := parseFBPrepResponse(responseText, []string{s3Key})
	if err != nil || len(newItems) == 0 {
		return nil, fmt.Errorf("feedback: failed to parse response: %w", err)
	}

	// Update only the target item in the job.
	updatedItems := make([]store.FBPrepItem, len(job.Items))
	copy(updatedItems, job.Items)
	updatedItems[itemIndex] = newItems[0]
	updatedItems[itemIndex].ItemIndex = itemIndex // Preserve correct index

	now := time.Now().UTC().Format(time.RFC3339)
	updatedJob := &store.FBPrepJob{
		ID:        jobID,
		Status:    "complete",
		Items:     updatedItems,
		MediaKeys: job.MediaKeys,
		CreatedAt: job.CreatedAt,
		UpdatedAt: now,
	}
	if err := sessionStore.PutFBPrepJob(ctx, sessionID, updatedJob); err != nil {
		return nil, fmt.Errorf("feedback: failed to save updated job: %w", err)
	}

	log.Info().
		Str("sessionId", sessionID).
		Str("jobId", jobID).
		Int("itemIndex", itemIndex).
		Msg("FB prep feedback regeneration complete")

	return &FBPrepOutput{
		SessionID: sessionID,
		Status:    "complete",
	}, nil
}

// handleCollectBatch reads the completed Gemini Batch job, parses results, and writes
// the final completed FBPrepJob to DynamoDB (DDR-082: economy mode via FBPrepPipeline SFN).
func handleCollectBatch(ctx context.Context, m map[string]interface{}) (*FBPrepOutput, error) {
	sessionID, _ := m["sessionId"].(string)
	jobID, _ := m["jobId"].(string)
	batchJobID, _ := m["batchJobId"].(string)

	if sessionID == "" || jobID == "" || batchJobID == "" {
		return nil, fmt.Errorf("collect-batch: sessionId, jobId, and batchJobId are required")
	}
	if sessionStore == nil {
		return nil, fmt.Errorf("collect-batch: session store not configured")
	}

	// Load existing job to get MediaKeys and CreatedAt.
	job, err := sessionStore.GetFBPrepJob(ctx, sessionID, jobID)
	if err != nil || job == nil {
		return nil, fmt.Errorf("collect-batch: job not found: %w", err)
	}

	collectClient, err := ai.NewAIClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect-batch: failed to initialize AI client: %w", err)
	}

	batchStatus, err := ai.CheckGeminiBatch(ctx, collectClient, batchJobID)
	if err != nil {
		return nil, fmt.Errorf("collect-batch: failed to check batch: %w", err)
	}
	if batchStatus.State != "JOB_STATE_SUCCEEDED" {
		return nil, fmt.Errorf("collect-batch: unexpected batch state %s", batchStatus.State)
	}
	if len(batchStatus.Results) == 0 {
		return nil, fmt.Errorf("collect-batch: no results in batch response")
	}

	result := batchStatus.Results[0]
	if result.Error != "" {
		return nil, fmt.Errorf("collect-batch: batch request failed: %s", result.Error)
	}
	if result.Response == nil {
		return nil, fmt.Errorf("collect-batch: nil response in batch result")
	}

	responseText := result.Response.Text()
	if responseText == "" {
		return nil, fmt.Errorf("collect-batch: empty response text")
	}

	var inputTokens, outputTokens int
	if result.Response.UsageMetadata != nil {
		inputTokens = int(result.Response.UsageMetadata.PromptTokenCount)
		outputTokens = int(result.Response.UsageMetadata.CandidatesTokenCount)
	}

	items, err := parseFBPrepResponse(responseText, job.MediaKeys)
	if err != nil {
		return nil, fmt.Errorf("collect-batch: failed to parse response: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_ = sessionStore.PutFBPrepJob(ctx, sessionID, &store.FBPrepJob{
		ID:           jobID,
		Status:       "complete",
		Items:        items,
		MediaKeys:    job.MediaKeys,
		BatchJobID:   batchJobID,
		EconomyMode:  true,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    now,
	})

	log.Info().
		Str("sessionId", sessionID).
		Str("jobId", jobID).
		Int("itemCount", len(items)).
		Int("inputTokens", inputTokens).
		Int("outputTokens", outputTokens).
		Msg("FB prep batch collection complete")

	return &FBPrepOutput{SessionID: sessionID, Status: "complete"}, nil
}

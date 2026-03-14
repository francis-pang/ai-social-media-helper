package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"

	"github.com/fpang/ai-social-media-helper/internal/assets"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/httputil"
	"github.com/fpang/ai-social-media-helper/internal/media"
	"github.com/fpang/ai-social-media-helper/internal/metrics"
	"github.com/fpang/ai-social-media-helper/internal/s3util"
	"github.com/fpang/ai-social-media-helper/internal/store"
)

const maxPresignedURLBytes int64 = 10 * 1024 * 1024 // 10 MiB (DDR-060)

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
		if t, _ := m["type"].(string); t == "fb-prep-mark-error" {
			return handleMarkError(ctx, m)
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
		// Pre-enrich location tags via a fast real-time Maps call before building the batch
		// JSONL. The GoogleMaps tool is not supported in Vertex AI batch (DDR-085).
		locationTags, locErr := resolveLocationTags(ctx, input.MediaItems, genaiClient)
		if locErr != nil {
			log.Warn().Err(locErr).Msg("Location pre-enrichment failed; proceeding with GPS-only metadata")
			locationTags = nil
		}

		parts, metadataCtx, s3Keys, err := buildFBPrepMediaParts(ctx, input.SessionID, input.MediaItems, genaiClient)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare media: %w", err)
		}
		prompt := buildFBPrepPrompt(metadataCtx, locationTags)
		parts = append(parts, &genai.Part{Text: prompt})
		// GoogleMaps tool is not supported in Vertex AI batch prediction — omit it.
		// The JSONL format rejects empty struct fields (googleMaps: {}) at import time.
		config := &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: assets.FBPrepSystemPrompt}},
			},
		}
		modelName := ai.GetBatchModelName()
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
			preEnrichStore := make(map[string]string, len(locationTags))
			for idx, tag := range locationTags {
				preEnrichStore[strconv.Itoa(idx)] = tag
			}
			_ = sessionStore.PutFBPrepJob(ctx, input.SessionID, &store.FBPrepJob{
				ID:                 jobID,
				Status:             "pending",
				BatchJobID:         batchJobID,
				MediaKeys:          s3Keys,
				EconomyMode:        true,
				PreEnrichLocations: preEnrichStore,
				CreatedAt:          now,
				UpdatedAt:          now,
			})
		}
		log.Info().
			Str("sessionId", input.SessionID).
			Str("batchJobId", batchJobID).
			Int("mediaCount", len(input.MediaItems)).
			Int("preEnrichedLocations", len(locationTags)).
			Msg("FB prep batch job submitted (economy mode)")
		return &FBPrepOutput{
			SessionID:  input.SessionID,
			Status:     "pending",
			BatchJobID: batchJobID,
		}, nil
	}

	// Build media parts and metadata context
	parts, metadataCtx, s3Keys, err := buildFBPrepMediaParts(ctx, input.SessionID, input.MediaItems, genaiClient)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare media: %w", err)
	}

	// Append metadata context as text. Real-time mode uses the GoogleMaps tool directly;
	// no pre-enrichment needed (locationTags=nil).
	prompt := buildFBPrepPrompt(metadataCtx, nil)
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

func buildFBPrepMediaParts(ctx context.Context, sessionID string, mediaItems []FBPrepMediaItem, genaiClient *genai.Client) ([]*genai.Part, string, []string, error) {
	var parts []*genai.Part
	var metaLines []string
	var s3Keys []string

	// Build filename -> FileResult map from session-scoped file processing (DDR-083).
	fileResultMap := make(map[string]store.FileResult)
	if fileProcessStore != nil {
		results, err := fileProcessStore.GetSessionFileResults(ctx, sessionID)
		if err == nil {
			for _, fr := range results {
				fileResultMap[fr.Filename] = fr
			}
		}
	}

	for i, item := range mediaItems {
		s3Keys = append(s3Keys, item.S3Key)
		ext := strings.ToLower(filepath.Ext(item.S3Key))
		filename := filepath.Base(item.S3Key)
		fr, hasFileResult := fileResultMap[filename]

		if item.MediaType == "image" || media.IsImage(ext) {
			// Prefer processedKey (downsized WebP) > ThumbnailKey > originalKey for presigned URL.
			useKey := item.S3Key
			mimeType := "image/jpeg"
			if hasFileResult {
				if fr.ProcessedKey != "" {
					useKey = fr.ProcessedKey
					if m, _ := media.GetMIMEType(strings.ToLower(filepath.Ext(fr.ProcessedKey))); m != "" {
						mimeType = m
					}
				} else if fr.ThumbnailKey != "" {
					useKey = fr.ThumbnailKey
					mimeType = "image/jpeg"
				}
				if fr.MimeType != "" {
					mimeType = fr.MimeType
				}
			} else {
				keyParts := strings.SplitN(item.S3Key, "/", 2)
				thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", keyParts[0], strings.TrimSuffix(filename, ext))
				processedKey := fmt.Sprintf("%s/processed/%s%s", keyParts[0], strings.TrimSuffix(filename, ext), ".webp")
				// Try processed first, then thumbnail
				if head, _ := s3Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &mediaBucket, Key: &processedKey}); head != nil {
					useKey = processedKey
					mimeType = "image/webp"
				} else if head, _ := s3Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &mediaBucket, Key: &thumbKey}); head != nil {
					useKey = thumbKey
				}
			}

			url, err := s3util.GeneratePresignedURL(ctx, presignClient, mediaBucket, useKey, 15*time.Minute)
			if err != nil {
				log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to generate presigned URL for image")
				continue
			}
			imgData, err := httputil.FetchURLToBytes(ctx, url)
			if err != nil {
				log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to fetch image from presigned URL")
				continue
			}
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{MIMEType: mimeType, Data: imgData},
			})
		} else if item.MediaType == "video" || media.IsVideo(ext) {
			// Prefer processedKey (pre-compressed WebM) > originalKey.
			useKey := item.S3Key
			mimeType := "video/webm"
			fileSize := fr.FileSize
			if hasFileResult {
				if fr.ProcessedKey != "" {
					useKey = fr.ProcessedKey
					mimeType = "video/webm"
				}
				if fr.MimeType != "" {
					mimeType = fr.MimeType
				}
			} else {
				keyParts := strings.SplitN(item.S3Key, "/", 2)
				processedKey := fmt.Sprintf("%s/processed/%s.webm", keyParts[0], strings.TrimSuffix(filename, ext))
				if head, _ := s3Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &mediaBucket, Key: &processedKey}); head != nil && head.ContentLength != nil {
					useKey = processedKey
					fileSize = *head.ContentLength
				} else if head, _ := s3Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &mediaBucket, Key: &item.S3Key}); head != nil && head.ContentLength != nil {
					fileSize = *head.ContentLength
				}
			}

			url, err := s3util.GeneratePresignedURL(ctx, presignClient, mediaBucket, useKey, 15*time.Minute)
			if err != nil {
				log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to generate presigned URL for video")
				continue
			}

		vertexAI := os.Getenv("VERTEX_AI_PROJECT") != ""
		if fileSize <= maxPresignedURLBytes || vertexAI {
			// Within size limit, or running on Vertex AI where Files.Upload is unsupported —
			// let Gemini fetch the video directly from the S3 presigned URL (DDR-060).
			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{MIMEType: mimeType, FileURI: url},
			})
		} else if genaiClient != nil {
			// Gemini Developer API only: upload large videos via Files API.
			tmpPath, tmpCleanup, err := httputil.FetchURLToFile(ctx, url)
			if err != nil {
				log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to download video for Files API upload")
				continue
			}
			uploaded, err := ai.UploadVideoToGeminiFiles(ctx, genaiClient, tmpPath, mimeType)
			tmpCleanup()
			if err != nil {
				log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to upload video to Gemini Files API")
				continue
			}
			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{MIMEType: uploaded.MIMEType, FileURI: uploaded.URI},
			})
		} else {
				// Fallback: download original and compress with CompressVideoForCaptions.
				tmpPath, cleanup, err := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, useKey)
				if err != nil {
					log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to download video")
					continue
				}
				var videoMeta *media.VideoMetadata
				if item.GPS != nil {
					videoMeta = &media.VideoMetadata{
						Latitude: item.GPS.Latitude, Longitude: item.GPS.Longitude, HasGPS: true,
					}
				}
				compressedPath, _, compCleanup, compErr := media.CompressVideoForCaptions(ctx, tmpPath, videoMeta)
				cleanup()
				if compErr != nil {
					log.Warn().Str("key", useKey).Err(compErr).Msg("Skipping: video compression failed")
					continue
				}
				data, readErr := os.ReadFile(compressedPath)
				compCleanup()
				if readErr != nil {
					log.Warn().Str("key", useKey).Err(readErr).Msg("Skipping: failed to read compressed video")
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

// buildFBPrepPrompt builds the user-turn prompt from the metadata context.
// locationTags (DDR-085) is an optional map of item index → Maps-verified place name
// to inject into the prompt as a supplementary section, bypassing the unavailable
// GoogleMaps tool in Vertex AI batch prediction. Pass nil for the real-time path.
func buildFBPrepPrompt(metadataCtx string, locationTags map[int]string) string {
	base := "## Metadata context\n\n" + metadataCtx
	if len(locationTags) > 0 {
		indices := make([]int, 0, len(locationTags))
		for i := range locationTags {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		lines := make([]string, 0, len(indices))
		for _, i := range indices {
			lines = append(lines, fmt.Sprintf("Item %d location (Maps-verified): %s", i, locationTags[i]))
		}
		base += "\n\n## Maps-verified locations\n" + strings.Join(lines, "\n")
	}
	return base + "\n\nGenerate the JSON array for each item in the same order as above."
}

// resolveLocationTags makes a single fast real-time Gemini call with the GoogleMaps tool
// to reverse-geocode GPS coordinates for each media item (DDR-085). Returns a map of
// item index → verified place name. Returns nil on any error so the caller can fall
// back to GPS-only metadata without blocking batch submission.
func resolveLocationTags(ctx context.Context, mediaItems []FBPrepMediaItem, client *genai.Client) (map[int]string, error) {
	type gpsItem struct {
		index int
		lat   float64
		lon   float64
	}
	var gpsItems []gpsItem
	for i, item := range mediaItems {
		if item.GPS != nil {
			gpsItems = append(gpsItems, gpsItem{i, item.GPS.Latitude, item.GPS.Longitude})
		}
	}
	if len(gpsItems) == 0 {
		return nil, nil
	}

	coordLines := make([]string, 0, len(gpsItems))
	for _, g := range gpsItems {
		coordLines = append(coordLines, fmt.Sprintf("Item %d: GPS %.6f, %.6f", g.index, g.lat, g.lon))
	}
	prompt := "Use Google Maps to reverse-geocode each GPS coordinate and return the best location name for a Facebook location tag.\n\n" +
		strings.Join(coordLines, "\n") +
		"\n\nReturn a JSON array only: [{\"index\": 0, \"location_tag\": \"Place Name, City, Country\"}, ...]"

	start := time.Now()
	resp, err := client.Models.GenerateContent(ctx, ai.GetModelName(),
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: prompt}}}},
		&genai.GenerateContentConfig{
			Tools: []*genai.Tool{{GoogleMaps: &genai.GoogleMaps{}}},
		},
	)
	elapsed := time.Since(start)

	m := metrics.New("AiSocialMedia").
		Dimension("Operation", "fbPrepLocationPreEnrich").
		Metric("LocationEnrichmentMs", float64(elapsed.Milliseconds()), metrics.UnitMilliseconds).
		Metric("LocationEnrichmentItemCount", float64(len(gpsItems)), metrics.UnitCount)
	if err != nil {
		m.Count("LocationEnrichmentFailure").Flush()
		return nil, fmt.Errorf("pre-enrichment call: %w", err)
	}
	if resp != nil && resp.UsageMetadata != nil {
		m.Metric("GeminiInputTokens", float64(resp.UsageMetadata.PromptTokenCount), metrics.UnitCount)
		m.Metric("GeminiOutputTokens", float64(resp.UsageMetadata.CandidatesTokenCount), metrics.UnitCount)
	}
	m.Count("LocationEnrichmentSuccess").Flush()

	raw := strings.TrimSpace(resp.Text())
	if start := strings.Index(raw, "["); start > 0 {
		raw = raw[start:]
	}
	if end := strings.LastIndex(raw, "]"); end >= 0 {
		raw = raw[:end+1]
	}

	var parsed []struct {
		Index       int    `json:"index"`
		LocationTag string `json:"location_tag"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		log.Warn().Err(err).Str("raw", raw).Msg("Failed to parse location pre-enrichment JSON")
		return nil, fmt.Errorf("parse pre-enrichment response: %w", err)
	}

	result := make(map[int]string, len(parsed))
	for _, p := range parsed {
		if p.LocationTag != "" {
			result[p.Index] = p.LocationTag
		}
	}
	log.Info().
		Int("resolvedCount", len(result)).
		Dur("duration", elapsed).
		Msg("Location pre-enrichment complete")
	return result, nil
}

// handleMarkError writes status:"error" to DynamoDB for the given job. Invoked by the
// FBPrepPipeline SFN catch handler when GeminiBatchPollPipeline fails (DDR-085).
func handleMarkError(ctx context.Context, m map[string]interface{}) (*FBPrepOutput, error) {
	sessionID, _ := m["sessionId"].(string)
	jobID, _ := m["jobId"].(string)
	log.Warn().Str("sessionId", sessionID).Str("jobId", jobID).Msg("Marking FB prep job as error (batch poll failure)")
	if sessionStore != nil && sessionID != "" && jobID != "" {
		now := time.Now().UTC().Format(time.RFC3339)
		_ = sessionStore.PutFBPrepJob(ctx, sessionID, &store.FBPrepJob{
			ID:        jobID,
			Status:    "error",
			Error:     "Batch prediction job failed",
			UpdatedAt: now,
		})
	}
	return &FBPrepOutput{SessionID: sessionID, Status: "error"}, nil
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
		// Vertex AI batch may return one JSON object per result line (not an array).
		var single fbPrepResponseItem
		if singleErr := json.Unmarshal([]byte(text), &single); singleErr == nil {
			raw = []fbPrepResponseItem{single}
		} else {
			// Or JSONL: one JSON object per line within a single response.
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var obj fbPrepResponseItem
				if json.Unmarshal([]byte(line), &obj) == nil {
					raw = append(raw, obj)
				}
			}
			if len(raw) == 0 {
				return nil, fmt.Errorf("parse JSON: %w", err)
			}
		}
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

	genaiClient, err := ai.NewAIClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("feedback: failed to initialize AI client: %w", err)
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

	parts, _, _, err := buildFBPrepMediaParts(ctx, sessionID, []FBPrepMediaItem{targetMediaItem}, genaiClient)
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

	// Merge all batch results — Vertex AI may return one output line per input item
	// (e.g. 6 items → 6 results). Previously we only used Results[0], losing items 1–5.
	var allItems []store.FBPrepItem
	var inputTokens, outputTokens int
	for i, result := range batchStatus.Results {
		if result.Error != "" {
			return nil, fmt.Errorf("collect-batch: batch result %d failed: %s", i, result.Error)
		}
		if result.Response == nil {
			return nil, fmt.Errorf("collect-batch: nil response in batch result %d", i)
		}
		responseText := result.Response.Text()
		if responseText == "" {
			continue // skip empty responses (e.g. from malformed output)
		}
		if result.Response.UsageMetadata != nil {
			inputTokens += int(result.Response.UsageMetadata.PromptTokenCount)
			outputTokens += int(result.Response.UsageMetadata.CandidatesTokenCount)
		}
		parsed, err := parseFBPrepResponse(responseText, job.MediaKeys)
		if err != nil {
			return nil, fmt.Errorf("collect-batch: failed to parse result %d: %w", i, err)
		}
		allItems = append(allItems, parsed...)
	}

	// Deduplicate by item_index (keep first occurrence) and sort for stable ordering.
	seen := make(map[int]bool)
	var items []store.FBPrepItem
	for _, it := range allItems {
		if seen[it.ItemIndex] {
			continue
		}
		seen[it.ItemIndex] = true
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ItemIndex < items[j].ItemIndex })
	if len(items) == 0 {
		return nil, fmt.Errorf("collect-batch: no items parsed from %d batch result(s)", len(batchStatus.Results))
	}

	// DDR-088: Emit token metrics for cost analysis.
	if inputTokens > 0 || outputTokens > 0 {
		metrics.New("AiSocialMedia").
			Dimension("Operation", "fbPrepBatch").
			Metric("GeminiInputTokens", float64(inputTokens), metrics.UnitCount).
			Metric("GeminiOutputTokens", float64(outputTokens), metrics.UnitCount).
			Flush()
	}

	// Compare batch model location tags against pre-enrichment values (DDR-085).
	// Emits CloudWatch metrics so we can evaluate whether to keep the pre-enrichment call.
	if len(job.PreEnrichLocations) > 0 {
		matchCount, mismatchCount := 0, 0
		for _, item := range items {
			preEnrich := job.PreEnrichLocations[strconv.Itoa(item.ItemIndex)]
			if preEnrich == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(preEnrich), strings.TrimSpace(item.LocationTag)) {
				matchCount++
			} else {
				mismatchCount++
				log.Info().
					Int("itemIndex", item.ItemIndex).
					Str("preEnrichLocation", preEnrich).
					Str("batchLocation", item.LocationTag).
					Msg("Location tag differs between pre-enrichment and batch model")
			}
		}
		total := matchCount + mismatchCount
		if total > 0 {
			agreementRate := float64(matchCount) / float64(total) * 100
			metrics.New("AiSocialMedia").
				Dimension("Operation", "fbPrepLocationComparison").
				Metric("LocationTagMatchCount", float64(matchCount), metrics.UnitCount).
				Metric("LocationTagMismatchCount", float64(mismatchCount), metrics.UnitCount).
				Metric("LocationTagAgreementRate", agreementRate, metrics.UnitNone).
				Property("sessionId", sessionID).
				Property("jobId", jobID).
				Flush()
		}
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

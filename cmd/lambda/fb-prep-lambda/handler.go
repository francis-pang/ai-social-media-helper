package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"

	"github.com/fpang/ai-social-media-helper/internal/assets"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/fbprep"
	"github.com/fpang/ai-social-media-helper/internal/httputil"
	"github.com/fpang/ai-social-media-helper/internal/media"
	"github.com/fpang/ai-social-media-helper/internal/metrics"
	"github.com/fpang/ai-social-media-helper/internal/s3util"
	"github.com/fpang/ai-social-media-helper/internal/store"
)

const maxPresignedURLBytes int64 = 10 * 1024 * 1024 // 10 MiB (DDR-060)

func handler(ctx context.Context, event interface{}) (out *FBPrepOutput, retErr error) {
	// Check for special event types before attempting batch normalization.
	if m, ok := event.(map[string]interface{}); ok {
		if t, _ := m["type"].(string); t == "fb-prep-feedback" {
			return handleFeedback(ctx, m)
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

	// Economy mode: prepare batches and return videos_to_upload for Map (one Lambda per video).
	// Step Functions Map uploads each video to GCS, then fb-prep-submit-batch submits to Vertex AI.
	if input.EconomyMode {
		locationTags, locErr := resolveLocationTags(ctx, input.MediaItems, genaiClient)
		if locErr != nil {
			log.Warn().Err(locErr).Msg("Location pre-enrichment failed; proceeding with GPS-only metadata")
			locationTags = nil
		}

		jobID := input.JobID
		if jobID == "" {
			jobID = "fbprep-" + uuid.New().String()[:8]
		}

		batches := buildFBPrepBatches(input.MediaItems)
		videosToUpload, batchesMeta, allS3Keys, _, err := buildFBPrepPrepareOutput(ctx, input.SessionID, batches, jobID)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare batches: %w", err)
		}

		if sessionStore != nil {
			now := time.Now().UTC().Format(time.RFC3339)
			preEnrichStore := make(map[string]string, len(locationTags))
			for idx, tag := range locationTags {
				preEnrichStore[strconv.Itoa(idx)] = tag
			}
			_ = sessionStore.PutFBPrepJob(ctx, input.SessionID, &store.FBPrepJob{
				ID:                 jobID,
				Status:             "pending",
				MediaKeys:          allS3Keys,
				EconomyMode:        true,
				PreEnrichLocations: preEnrichStore,
				CreatedAt:          now,
				UpdatedAt:          now,
			})
		}

		log.Info().
			Str("sessionId", input.SessionID).
			Str("jobId", jobID).
			Int("videoCount", len(videosToUpload)).
			Int("batchCount", len(batches)).
			Msg("FB prep prepare complete; videos_to_upload for Map")

		return &FBPrepOutput{
			SessionID:      input.SessionID,
			Status:         "pending",
			JobID:          jobID,
			VideosToUpload: videosToUpload,
			BatchesMeta:    batchesMeta,
			LocationTags:   locationTagsToMap(locationTags),
		}, nil
	}

	// Build media parts and metadata context
	parts, metadataCtx, s3Keys, _, err := buildFBPrepMediaParts(ctx, input.SessionID, input.MediaItems, genaiClient, false, "", 0)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare media: %w", err)
	}

	// Append metadata context as text. Real-time mode uses the GoogleMaps tool directly;
	// no pre-enrichment needed (locationTags=nil).
	prompt := fbprep.BuildPrompt(metadataCtx, nil)
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

	items, err := fbprep.ParseResponse(responseText, s3Keys)
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

// buildFBPrepMediaParts builds genai.Part slices for each media item.
// When forBatch is true, videos are uploaded to GCS (gs://) instead of presigned URLs,
// since Vertex AI limits HTTP URLs to 1 video per request but allows up to 10 via GCS.
// jobID is used for GCS path prefix when forBatch is true (e.g. fb-prep-videos/{jobID}/{uuid}.webm).
// baseIndex is the global item index offset for metadata (e.g. batch 1 uses baseIndex=8 so "Item 8", "Item 9"...).
// Returns: parts, metadataCtx, s3Keys, gcsPathsForCleanup, error.
func buildFBPrepMediaParts(ctx context.Context, sessionID string, mediaItems []FBPrepMediaItem, genaiClient *genai.Client, forBatch bool, jobID string, baseIndex int) ([]*genai.Part, string, []string, []string, error) {
	var parts []*genai.Part
	var metaLines []string
	var s3Keys []string
	var gcsPaths []string

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
			// Use downscaled video from processing screen (processedKey). Thumbnail and downscaling
			// are done in the upload/processing pipeline before FB Prep.
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

			if forBatch {
				// Batch mode: upload downscaled video (from S3) to GCS so Vertex AI can accept up to 10 per request.
				data, err := getVideoBytesFromS3(ctx, presignClient, mediaBucket, useKey)
				if err != nil {
					log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to download video for GCS batch")
					continue
				}
				gcsBucket := os.Getenv("GCS_BATCH_BUCKET")
				if gcsBucket == "" {
					log.Warn().Str("key", useKey).Msg("Skipping: GCS_BATCH_BUCKET not set for batch video")
					continue
				}
				objectPath := fmt.Sprintf("fb-prep-videos/%s/%s.webm", jobID, uuid.New().String())
				gsURI, err := ai.UploadVideoToGCS(ctx, gcsBucket, objectPath, data, "video/webm")
				if err != nil {
					log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to upload video to GCS")
					continue
				}
				gcsPaths = append(gcsPaths, gsURI)
				parts = append(parts, &genai.Part{
					FileData: &genai.FileData{MIMEType: "video/webm", FileURI: gsURI},
				})
			} else {
				// Non-batch: use presigned URL, Files API, or inline to the downscaled video from S3.
				url, err := s3util.GeneratePresignedURL(ctx, presignClient, mediaBucket, useKey, 15*time.Minute)
				if err != nil {
					log.Warn().Err(err).Str("key", useKey).Msg("Skipping: failed to generate presigned URL for video")
					continue
				}
				vertexAI := os.Getenv("VERTEX_AI_PROJECT") != ""
				if fileSize <= maxPresignedURLBytes || vertexAI {
					parts = append(parts, &genai.Part{
						FileData: &genai.FileData{MIMEType: mimeType, FileURI: url},
					})
				} else if genaiClient != nil {
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
		}

		// Metadata line for this item (use baseIndex+i for correct global item_index in batch mode)
		metaLines = append(metaLines, fmt.Sprintf("Item %d (%s):", baseIndex+i, item.Filename))
		if item.GPS != nil {
			metaLines = append(metaLines, fmt.Sprintf("  GPS: %.6f, %.6f", item.GPS.Latitude, item.GPS.Longitude))
		}
		if item.DateTaken != "" {
			metaLines = append(metaLines, fmt.Sprintf("  Date: %s", item.DateTaken))
		}
	}

	metadataCtx := strings.Join(metaLines, "\n")
	return parts, metadataCtx, s3Keys, gcsPaths, nil
}

// buildFBPrepPrepareOutput builds videos_to_upload (one per video for Map) and batches_meta.
// Does NOT upload to GCS; each video is uploaded by a separate Lambda invocation.
func buildFBPrepPrepareOutput(ctx context.Context, sessionID string, batches [][]FBPrepMediaItem, jobID string) ([]VideoToUpload, []FBPrepBatchMeta, []string, []string, error) {
	var videosToUpload []VideoToUpload
	var batchesMeta []FBPrepBatchMeta
	var allS3Keys []string
	var allGCSPaths []string // Will be filled from Map results in submit; placeholder for now

	fileResultMap := make(map[string]store.FileResult)
	if fileProcessStore != nil {
		results, err := fileProcessStore.GetSessionFileResults(ctx, sessionID)
		if err == nil {
			for _, fr := range results {
				fileResultMap[fr.Filename] = fr
			}
		}
	}

	baseIdx := 0
	for batchIdx, batch := range batches {
		var metaLines []string
		var s3Keys []string
		for i, item := range batch {
			s3Keys = append(s3Keys, item.S3Key)
			ext := strings.ToLower(filepath.Ext(item.S3Key))
			filename := filepath.Base(item.S3Key)
			fr, hasFileResult := fileResultMap[filename]

			if item.MediaType == "video" || media.IsVideo(ext) {
				useKey := item.S3Key
				if hasFileResult && fr.ProcessedKey != "" {
					useKey = fr.ProcessedKey
				} else if !hasFileResult {
					keyParts := strings.SplitN(item.S3Key, "/", 2)
					processedKey := fmt.Sprintf("%s/processed/%s.webm", keyParts[0], strings.TrimSuffix(filename, ext))
					if head, _ := s3Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &mediaBucket, Key: &processedKey}); head != nil {
						useKey = processedKey
					}
				}
				videosToUpload = append(videosToUpload, VideoToUpload{
					S3Key:            item.S3Key,
					UseKey:           useKey,
					JobID:            jobID,
					BatchIndex:       batchIdx,
					ItemIndexInBatch: i,
				})
			}

			metaLines = append(metaLines, fmt.Sprintf("Item %d (%s):", baseIdx+i, item.Filename))
			if item.GPS != nil {
				metaLines = append(metaLines, fmt.Sprintf("  GPS: %.6f, %.6f", item.GPS.Latitude, item.GPS.Longitude))
			}
			if item.DateTaken != "" {
				metaLines = append(metaLines, fmt.Sprintf("  Date: %s", item.DateTaken))
			}
		}
		metadataCtx := strings.Join(metaLines, "\n")
		batchesMeta = append(batchesMeta, FBPrepBatchMeta{
			BatchIndex:  batchIdx,
			MediaItems:  batch,
			MetadataCtx: metadataCtx,
			BaseIndex:   baseIdx,
			S3Keys:      s3Keys,
		})
		allS3Keys = append(allS3Keys, s3Keys...)
		baseIdx += len(batch)
	}
	return videosToUpload, batchesMeta, allS3Keys, allGCSPaths, nil
}

func locationTagsToMap(tags map[int]string) map[string]string {
	if tags == nil {
		return nil
	}
	out := make(map[string]string)
	for k, v := range tags {
		out[strconv.Itoa(k)] = v
	}
	return out
}

// buildFBPrepBatches splits media items into batches of up to 10 videos each.
// Media stay in alphabetically numeric order (by S3 key). Each batch contains up to 10 videos;
// images have no limit. Example: 3 videos, 5 images, 4 videos → Batch1: items 0–7, Batch2: items 8–11.
func buildFBPrepBatches(mediaItems []FBPrepMediaItem) [][]FBPrepMediaItem {
	var batches [][]FBPrepMediaItem
	var current []FBPrepMediaItem
	videoCount := 0
	for _, item := range mediaItems {
		ext := strings.ToLower(filepath.Ext(item.S3Key))
		isVideo := item.MediaType == "video" || media.IsVideo(ext)
		if isVideo && videoCount >= 10 {
			batches = append(batches, current)
			current = nil
			videoCount = 0
		}
		current = append(current, item)
		if isVideo {
			videoCount++
		}
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// getVideoBytesFromS3 downloads the downscaled video from S3 and returns bytes for GCS upload.
// Thumbnail and downscaling are done in the upload/processing pipeline before FB Prep.
func getVideoBytesFromS3(ctx context.Context, presignClient *s3.PresignClient, bucket, key string) ([]byte, error) {
	url, err := s3util.GeneratePresignedURL(ctx, presignClient, bucket, key, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	tmpPath, cleanup, err := httputil.FetchURLToFile(ctx, url)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return os.ReadFile(tmpPath)
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
// FBPrepPipeline SFN catch handler when GeminiBatchPollPipeline or CollectBatchResults fails.
// Writes the actual error cause (from collectError or batchError) to FBPrepJob.Error instead
// of the generic "Batch prediction job failed".
func handleMarkError(ctx context.Context, m map[string]interface{}) (*FBPrepOutput, error) {
	sessionID, _ := m["sessionId"].(string)
	jobID, _ := m["jobId"].(string)
	errorMsg := "Batch prediction job failed"
	if ce, ok := m["collectError"].(map[string]interface{}); ok {
		if cause, _ := ce["Cause"].(string); cause != "" {
			errorMsg = cause
		}
	}
	if errorMsg == "Batch prediction job failed" {
		if be, ok := m["batchError"].(map[string]interface{}); ok {
			if cause, _ := be["Cause"].(string); cause != "" {
				errorMsg = cause
			}
		}
	}
	log.Warn().Str("sessionId", sessionID).Str("jobId", jobID).Str("error", errorMsg).Msg("Marking FB prep job as error")
	if sessionStore != nil && sessionID != "" && jobID != "" {
		now := time.Now().UTC().Format(time.RFC3339)
		_ = sessionStore.PutFBPrepJob(ctx, sessionID, &store.FBPrepJob{
			ID:        jobID,
			Status:    "error",
			Error:     errorMsg,
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

	parts, _, _, _, err := buildFBPrepMediaParts(ctx, sessionID, []FBPrepMediaItem{targetMediaItem}, genaiClient, false, "", 0)
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
	newItems, err := fbprep.ParseResponse(responseText, []string{s3Key})
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

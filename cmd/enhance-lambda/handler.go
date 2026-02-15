package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/s3util"
	"github.com/fpang/gemini-media-cli/internal/store"
)

func handleEnhance(ctx context.Context, event EnhanceEvent) (EnhanceResult, error) {
	handlerStart := time.Now()
	bucket := mediaBucket
	if event.Bucket != "" {
		bucket = event.Bucket
	}

	logger := log.With().
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Str("key", event.Key).
		Int("itemIndex", event.ItemIndex).
		Logger()

	logger.Info().Msg("Starting photo enhancement")

	// Validate input.
	if event.SessionID == "" || event.JobID == "" || event.Key == "" {
		return EnhanceResult{
			OriginalKey: event.Key,
			Error:       "sessionId, jobId, and key are required",
		}, fmt.Errorf("sessionId, jobId, and key are required")
	}

	// Download photo from S3.
	tmpPath, cleanup, err := s3util.DownloadToTempFile(ctx, s3Client, bucket, event.Key)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to download photo")
		updateItemError(ctx, event, "download failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("download failed: %v", err),
		}, err
	}
	defer cleanup()

	// Read image data.
	imageData, err := os.ReadFile(tmpPath)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to read photo")
		updateItemError(ctx, event, "read failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("read failed: %v", err),
		}, err
	}

	// Determine MIME type.
	ext := strings.ToLower(filepath.Ext(event.Key))
	mime := "image/jpeg"
	if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
		mime = m
	}
	logger.Debug().Str("mimeType", mime).Str("extension", ext).Msg("MIME type determined")

	// Get image dimensions for mask generation.
	imgConfig, _, configErr := image.DecodeConfig(bytes.NewReader(imageData))
	imageWidth := 1024
	imageHeight := 1024
	if configErr == nil {
		imageWidth = imgConfig.Width
		imageHeight = imgConfig.Height
	}
	logger.Debug().Int("width", imageWidth).Int("height", imageHeight).Bool("decoded", configErr == nil).Msg("Image dimensions")

	// Initialize Gemini client.
	apiKey := os.Getenv("GEMINI_API_KEY")
	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create Gemini client")
		updateItemError(ctx, event, "Gemini client initialization failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("Gemini client failed: %v", err),
		}, err
	}
	geminiImageClient := chat.NewGeminiImageClient(genaiClient)

	// Set up Imagen client (optional — only if Vertex AI is configured).
	var imagenClient *chat.ImagenClient
	vertexProject := os.Getenv("VERTEX_AI_PROJECT")
	vertexRegion := os.Getenv("VERTEX_AI_REGION")
	vertexToken := os.Getenv("VERTEX_AI_TOKEN")
	if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
		imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
	}
	logger.Debug().Bool("imagenConfigured", imagenClient != nil).Msg("Imagen client status")

	// Run the full enhancement pipeline.
	state, err := chat.RunFullEnhancement(ctx, geminiImageClient, imagenClient, imageData, mime, imageWidth, imageHeight)
	if err != nil {
		logger.Warn().Err(err).Msg("Enhancement pipeline failed")
		updateItemError(ctx, event, err.Error())
		result := EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       err.Error(),
		}
		if state != nil {
			result.Phase1Text = state.Phase1Text
		}
		return result, err
	}

	// Upload enhanced image to S3.
	enhancedKey := fmt.Sprintf("%s/enhanced/%s", event.SessionID, filepath.Base(event.Key))
	contentType := state.CurrentMIME
	if contentType == "" {
		contentType = mime
	}
	logger.Debug().Str("enhancedKey", enhancedKey).Int("size", len(state.CurrentData)).Msg("Uploading enhanced image to S3")
	_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &enhancedKey,
		Body:        bytes.NewReader(state.CurrentData),
		ContentType: &contentType,
		Tagging:     s3util.ProjectTagging(),
	})
	if uploadErr != nil {
		logger.Error().Err(uploadErr).Str("enhancedKey", enhancedKey).Msg("Failed to upload enhanced image")
		updateItemError(ctx, event, "upload failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("upload failed: %v", uploadErr),
		}, uploadErr
	}

	// Generate and upload thumbnail of enhanced version.
	enhancedThumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", event.SessionID,
		strings.TrimSuffix(filepath.Base(event.Key), filepath.Ext(event.Key)))
	thumbData, _, thumbErr := s3util.GenerateThumbnailFromBytes(state.CurrentData, contentType, thumbnailMaxDimension)
	if thumbErr == nil {
		thumbContentType := "image/jpeg"
		s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      &bucket,
			Key:         &enhancedThumbKey,
			Body:        bytes.NewReader(thumbData),
			ContentType: &thumbContentType,
			Tagging:     s3util.ProjectTagging(),
		})
	}

	// Update DynamoDB with the enhanced item results.
	updateItemComplete(ctx, event, enhancedKey, enhancedThumbKey, state)

	logger.Info().
		Str("enhancedKey", enhancedKey).
		Str("phase", state.Phase).
		Int("imagenEdits", state.ImagenEdits).
		Dur("duration", time.Since(handlerStart)).
		Msg("Photo enhancement complete")

	return EnhanceResult{
		OriginalKey:      event.Key,
		EnhancedKey:      enhancedKey,
		EnhancedThumbKey: enhancedThumbKey,
		Phase:            state.Phase,
		Phase1Text:       state.Phase1Text,
		ImagenEdits:      state.ImagenEdits,
	}, nil
}

// updateItemComplete updates the enhancement item in DynamoDB with success results.
// Best-effort — errors are logged but don't affect the Lambda response.
func updateItemComplete(ctx context.Context, event EnhanceEvent, enhancedKey, enhancedThumbKey string, state *chat.EnhancementState) {
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Warn().Err(err).Msg("Failed to get enhancement job for completion update")
		return
	}

	if event.ItemIndex >= 0 && event.ItemIndex < len(job.Items) {
		job.Items[event.ItemIndex].Phase = state.Phase
		job.Items[event.ItemIndex].EnhancedKey = enhancedKey
		job.Items[event.ItemIndex].EnhancedThumbKey = enhancedThumbKey
		job.Items[event.ItemIndex].OriginalThumbKey = fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(event.Key), filepath.Ext(event.Key)))
		job.Items[event.ItemIndex].Phase1Text = state.Phase1Text
		job.Items[event.ItemIndex].ImagenEdits = state.ImagenEdits
		if state.Analysis != nil {
			job.Items[event.ItemIndex].Analysis = &store.AnalysisResult{
				OverallAssessment:    state.Analysis.OverallAssessment,
				ProfessionalScore:    state.Analysis.ProfessionalScore,
				TargetScore:          state.Analysis.TargetScore,
				NoFurtherEditsNeeded: state.Analysis.NoFurtherEditsNeeded,
			}
			for _, imp := range state.Analysis.RemainingImprovements {
				job.Items[event.ItemIndex].Analysis.RemainingImprovements = append(
					job.Items[event.ItemIndex].Analysis.RemainingImprovements,
					store.ImprovementItem{
						Type:            imp.Type,
						Description:     imp.Description,
						Region:          imp.Region,
						Impact:          imp.Impact,
						ImagenSuitable:  imp.ImagenSuitable,
						EditInstruction: imp.EditInstruction,
					},
				)
			}
		}
		job.CompletedCount++
		if job.CompletedCount >= job.TotalCount {
			job.Status = "complete"
		}
		if err := sessionStore.PutEnhancementJob(ctx, event.SessionID, job); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement job with completion")
		}
	}
}

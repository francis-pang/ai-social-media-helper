// Package main provides a Lambda entry point for per-photo AI enhancement.
//
// This Lambda is invoked by the Step Functions EnhancementPipeline Map state —
// one invocation per photo. It downloads a single photo from S3, runs the
// multi-phase Gemini enhancement pipeline, uploads the enhanced version,
// and updates the enhancement job in DynamoDB.
//
// Container: Light (Dockerfile.light — no ffmpeg needed for photo enhancement)
// Memory: 2 GB
// Timeout: 5 minutes
//
// See DDR-031: Multi-Step Photo Enhancement Pipeline
// See DDR-035: Multi-Lambda Deployment Architecture
// See DDR-043: Step Functions Lambda Entrypoints
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

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// thumbnailMaxDimension is the max width/height for enhanced photo thumbnails.
const thumbnailMaxDimension = 400

// AWS clients and configuration initialized at cold start.
var (
	s3Client     *s3.Client
	sessionStore store.SessionStore
	mediaBucket  string
)

func init() {
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	s3Client = s3.NewFromConfig(cfg)
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	// Initialize DynamoDB store.
	tableName := os.Getenv("DYNAMO_TABLE_NAME")
	if tableName == "" {
		tableName = "media-selection-sessions"
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	sessionStore = store.NewDynamoStore(ddbClient, tableName)

	// Load Gemini API key from SSM Parameter Store if not set.
	if os.Getenv("GEMINI_API_KEY") == "" {
		ssmClient := ssm.NewFromConfig(cfg)
		paramName := os.Getenv("SSM_API_KEY_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/gemini-api-key"
		}
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
		}
		os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
		log.Info().Msg("Gemini API key loaded from SSM Parameter Store")
	}
}

// EnhanceEvent is the input payload from Step Functions.
// The Map state iterates over selected photo keys and sends one event per photo.
type EnhanceEvent struct {
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Key       string `json:"key"`
	ItemIndex int    `json:"itemIndex"`
	Bucket    string `json:"bucket,omitempty"`
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

func handler(ctx context.Context, event EnhanceEvent) (EnhanceResult, error) {
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
	tmpPath, cleanup, err := downloadFromS3(ctx, bucket, event.Key)
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

	// Get image dimensions for mask generation.
	imgConfig, _, configErr := image.DecodeConfig(bytes.NewReader(imageData))
	imageWidth := 1024
	imageHeight := 1024
	if configErr == nil {
		imageWidth = imgConfig.Width
		imageHeight = imgConfig.Height
	}

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
	_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &enhancedKey,
		Body:        bytes.NewReader(state.CurrentData),
		ContentType: &contentType,
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
	thumbData, _, thumbErr := generateThumbnailFromBytes(state.CurrentData, contentType, thumbnailMaxDimension)
	if thumbErr == nil {
		thumbContentType := "image/jpeg"
		s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      &bucket,
			Key:         &enhancedThumbKey,
			Body:        bytes.NewReader(thumbData),
			ContentType: &thumbContentType,
		})
	}

	// Update DynamoDB with the enhanced item results.
	updateItemComplete(ctx, event, enhancedKey, enhancedThumbKey, state)

	logger.Info().
		Str("enhancedKey", enhancedKey).
		Str("phase", state.Phase).
		Int("imagenEdits", state.ImagenEdits).
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

func main() {
	lambda.Start(handler)
}

// --- DynamoDB Helpers ---

// updateItemError updates the enhancement item in DynamoDB with an error status.
// Best-effort — errors are logged but don't affect the Lambda response.
func updateItemError(ctx context.Context, event EnhanceEvent, errMsg string) {
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Warn().Err(err).Msg("Failed to get enhancement job for error update")
		return
	}

	if event.ItemIndex >= 0 && event.ItemIndex < len(job.Items) {
		job.Items[event.ItemIndex].Phase = chat.PhaseError
		job.Items[event.ItemIndex].Error = errMsg
		job.CompletedCount++
		if err := sessionStore.PutEnhancementJob(ctx, event.SessionID, job); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement job with error")
		}
	}
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
		if err := sessionStore.PutEnhancementJob(ctx, event.SessionID, job); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement job with completion")
		}
	}
}

// --- S3 Helpers ---

// downloadFromS3 downloads an S3 object to a temp file and returns its path
// and a cleanup function. Caller must defer cleanup().
func downloadFromS3(ctx context.Context, bucket, key string) (string, func(), error) {
	tmpFile, err := os.CreateTemp("", "enhance-*"+filepath.Ext(key))
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := result.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return "", nil, fmt.Errorf("write: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return "", nil, fmt.Errorf("read: %w", readErr)
		}
	}
	tmpFile.Close()

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

// --- Thumbnail Helper ---

// generateThumbnailFromBytes creates a thumbnail from raw image bytes.
func generateThumbnailFromBytes(imageData []byte, mimeType string, maxDimension int) ([]byte, string, error) {
	tmpFile, err := os.CreateTemp("", "enhance-thumb-*")
	if err != nil {
		return nil, "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(imageData); err != nil {
		tmpFile.Close()
		return nil, "", err
	}
	tmpFile.Close()

	info, _ := os.Stat(tmpPath)
	mf := &filehandler.MediaFile{
		Path:     tmpPath,
		MIMEType: mimeType,
		Size:     info.Size(),
	}

	return filehandler.GenerateThumbnail(mf, maxDimension)
}

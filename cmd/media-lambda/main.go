// Package main provides a Lambda entry point for the media triage API.
//
// It wraps the same triage logic from the chat package behind API Gateway,
// using S3 for media storage instead of the local filesystem.
//
// Endpoints:
//
//	GET  /api/health               — health check
//	GET  /api/upload-url           — presigned S3 PUT URL for browser upload
//	POST /api/triage/start         — start triage from uploaded S3 files
//	GET  /api/triage/{id}/results  — poll triage results
//	POST /api/triage/{id}/confirm  — delete confirmed files from S3
//	GET  /api/media/thumbnail      — generate thumbnail from S3 object
//	GET  /api/media/full           — presigned GET URL for full-resolution image
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
)

// AWS clients initialized at cold start.
var (
	s3Client    *s3.Client
	presigner   *s3.PresignClient
	mediaBucket string
)

func init() {
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	s3Client = s3.NewFromConfig(cfg)
	presigner = s3.NewPresignClient(s3Client)
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	// Load Gemini API key from SSM Parameter Store if not set via env var.
	if os.Getenv("GEMINI_API_KEY") == "" {
		paramName := os.Getenv("SSM_API_KEY_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/gemini-api-key"
		}
		ssmClient := ssm.NewFromConfig(cfg)
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

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/upload-url", handleUploadURL)
	mux.HandleFunc("/api/triage/start", handleTriageStart)
	mux.HandleFunc("/api/triage/", handleTriageRoutes)
	mux.HandleFunc("/api/media/thumbnail", handleThumbnail)
	mux.HandleFunc("/api/media/full", handleFullImage)

	adapter := httpadapter.NewV2(mux)
	lambda.Start(adapter.ProxyWithContext)
}

// --- Health ---

func handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "ai-social-media-helper",
	})
}

// --- Presigned Upload URL ---

// GET /api/upload-url?sessionId=...&filename=...&contentType=...
// Returns a presigned S3 PUT URL so the browser can upload directly to S3.
func handleUploadURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	filename := r.URL.Query().Get("filename")
	contentType := r.URL.Query().Get("contentType")

	if sessionID == "" || filename == "" || contentType == "" {
		httpError(w, http.StatusBadRequest, "sessionId, filename, and contentType are required")
		return
	}

	// Sanitize filename
	filename = filepath.Base(filename)
	key := sessionID + "/" + filename

	result, err := presigner.PresignPutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &mediaBucket,
		Key:         &key,
		ContentType: &contentType,
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("Failed to generate presigned URL")
		httpError(w, http.StatusInternalServerError, "failed to generate upload URL")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"uploadUrl": result.URL,
		"key":       key,
	})
}

// --- Triage Job Management ---

type triageJob struct {
	mu        sync.Mutex
	id        string
	sessionID string
	status    string // "pending", "processing", "complete", "error"
	keep      []triageResultItem
	discard   []triageResultItem
	errMsg    string
}

type triageResultItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Key          string `json:"key"`
	Saveable     bool   `json:"saveable"`
	Reason       string `json:"reason"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

var (
	jobsMu sync.Mutex
	jobs   = make(map[string]*triageJob)
	jobSeq int
)

func newJob(sessionID string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	jobSeq++
	id := fmt.Sprintf("triage-%d", jobSeq)
	j := &triageJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	jobs[id] = j
	return j
}

func getJob(id string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	return jobs[id]
}

// --- Triage Start ---

// POST /api/triage/start
// Body: {"sessionId": "uuid", "model": "optional-model-name"}
func handleTriageStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Model     string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" {
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}

	model := chat.DefaultModelName
	if req.Model != "" {
		model = req.Model
	}

	job := newJob(req.SessionID)
	go runTriageJob(job, model)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

// --- Triage Routes ---

func handleTriageRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/triage/"), "/")
	if len(parts) < 2 {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	jobID := parts[0]
	if !strings.HasPrefix(jobID, "triage-") {
		jobID = "triage-" + jobID
	}
	action := parts[1]

	job := getJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "job not found")
		return
	}

	switch action {
	case "results":
		handleTriageResults(w, r, job)
	case "confirm":
		handleTriageConfirm(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/triage/{id}/results
func handleTriageResults(w http.ResponseWriter, r *http.Request, job *triageJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":      job.id,
		"status":  job.status,
		"keep":    job.keep,
		"discard": job.discard,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}

// POST /api/triage/{id}/confirm
func handleTriageConfirm(w http.ResponseWriter, r *http.Request, job *triageJob) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		DeleteKeys []string `json:"deleteKeys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := context.Background()
	var deleted int
	var errMsgs []string

	for _, key := range req.DeleteKeys {
		if !isValidDeleteKey(job, key) {
			errMsgs = append(errMsgs, fmt.Sprintf("key not in triage results: %s", key))
			continue
		}
		_, err := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: &mediaBucket,
			Key:    &key,
		})
		if err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("failed to delete %s: %v", key, err))
			continue
		}
		deleted++
		log.Info().Str("key", key).Msg("Deleted S3 object")
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"errors":         errMsgs,
		"reclaimedBytes": 0, // S3 doesn't report freed bytes synchronously
	})
}

// --- Media Endpoints ---

// GET /api/media/thumbnail?key=sessionId/filename.jpg
func handleThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "key is required")
		return
	}

	ext := strings.ToLower(filepath.Ext(key))

	// For images, download from S3, generate thumbnail, return bytes.
	if mime, ok := filehandler.SupportedImageExtensions[ext]; ok {
		tmpPath, cleanup, err := downloadFromS3(context.Background(), key)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to download for thumbnail")
			httpError(w, http.StatusNotFound, "file not found")
			return
		}
		defer cleanup()

		info, _ := os.Stat(tmpPath)
		mf := &filehandler.MediaFile{
			Path:     tmpPath,
			MIMEType: mime,
			Size:     info.Size(),
		}

		thumbData, thumbMIME, err := filehandler.GenerateThumbnail(mf, 400)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to generate thumbnail")
			httpError(w, http.StatusInternalServerError, "thumbnail generation failed")
			return
		}
		w.Header().Set("Content-Type", thumbMIME)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(thumbData)
		return
	}

	// For videos, return a placeholder SVG.
	if _, ok := filehandler.SupportedVideoExtensions[ext]; ok {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400" viewBox="0 0 400 400">
			<rect width="400" height="400" fill="#1a1d27"/>
			<polygon points="160,120 160,280 290,200" fill="#8b8fa8"/>
			<text x="200" y="340" text-anchor="middle" fill="#8b8fa8" font-size="16" font-family="sans-serif">%s</text>
		</svg>`, filepath.Base(key))
		return
	}

	httpError(w, http.StatusBadRequest, "unsupported file type")
}

// GET /api/media/full?key=sessionId/filename.jpg
// Returns a presigned GET URL for the full-resolution image.
func handleFullImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "key is required")
		return
	}

	result, err := presigner.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	}, s3.WithPresignExpires(1*time.Hour))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to generate download URL")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"url": result.URL,
	})
}

// --- Triage Processing ---

func runTriageJob(job *triageJob, model string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setJobError(job, "GEMINI_API_KEY not configured")
		return
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		setJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}
	defer client.Close()

	// List objects in the session prefix
	prefix := job.sessionID + "/"
	listResult, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &mediaBucket,
		Prefix: &prefix,
	})
	if err != nil {
		setJobError(job, fmt.Sprintf("Failed to list S3 objects: %v", err))
		return
	}

	if len(listResult.Contents) == 0 {
		setJobError(job, "No files found for session")
		return
	}

	log.Info().Int("count", len(listResult.Contents)).Str("session", job.sessionID).Msg("Found S3 objects for triage")

	// Download each file and create MediaFile objects
	tmpDir := filepath.Join(os.TempDir(), "triage", job.sessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir) // Clean up after triage

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string // Parallel slice tracking which key maps to which MediaFile

	for _, obj := range listResult.Contents {
		key := *obj.Key
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		if !filehandler.IsSupported(ext) {
			log.Debug().Str("key", key).Msg("Skipping unsupported file type")
			continue
		}

		localPath := filepath.Join(tmpDir, filename)
		if err := downloadToFile(ctx, key, localPath); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to download file")
			continue
		}

		mf, err := filehandler.LoadMediaFile(localPath)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to load media file")
			continue
		}

		allMediaFiles = append(allMediaFiles, mf)
		s3Keys = append(s3Keys, key)
	}

	if len(allMediaFiles) == 0 {
		setJobError(job, "No supported media files found in the uploaded session")
		return
	}

	log.Info().Int("count", len(allMediaFiles)).Msg("Starting Lambda triage evaluation")

	// Call the existing AskMediaTriage — reusing all Gemini interaction logic
	triageResults, err := chat.AskMediaTriage(ctx, client, allMediaFiles, model)
	if err != nil {
		setJobError(job, fmt.Sprintf("Triage failed: %v", err))
		return
	}

	// Map results to items with S3 keys and thumbnail URLs
	job.mu.Lock()
	for _, tr := range triageResults {
		idx := tr.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		key := s3Keys[idx]
		item := triageResultItem{
			Media:        tr.Media,
			Filename:     tr.Filename,
			Key:          key,
			Saveable:     tr.Saveable,
			Reason:       tr.Reason,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", key),
		}
		if tr.Saveable {
			job.keep = append(job.keep, item)
		} else {
			job.discard = append(job.discard, item)
		}
	}
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("keep", len(job.keep)).
		Int("discard", len(job.discard)).
		Msg("Lambda triage complete")
}

// --- S3 Helpers ---

// downloadFromS3 downloads an S3 object to a temp file and returns its path
// and a cleanup function. Caller must defer cleanup().
func downloadFromS3(ctx context.Context, key string) (string, func(), error) {
	tmpFile, err := os.CreateTemp("", "media-*"+filepath.Ext(key))
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	})
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	if _, err := io.Copy(tmpFile, result.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("download: %w", err)
	}
	tmpFile.Close()

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

// downloadToFile downloads an S3 object to a specific local path.
func downloadToFile(ctx context.Context, key, localPath string) error {
	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, result.Body); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	return nil
}

// --- Validation ---

func isValidDeleteKey(job *triageJob, key string) bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	for _, item := range job.discard {
		if item.Key == key {
			return true
		}
	}
	return false
}

func setJobError(job *triageJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Triage job failed")
}

// --- JSON Helpers ---

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func httpError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

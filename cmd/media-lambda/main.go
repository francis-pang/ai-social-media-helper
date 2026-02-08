// Package main provides a Lambda entry point for the media triage API.
//
// It wraps the same triage logic from the chat package behind API Gateway,
// using S3 for media storage instead of the local filesystem.
//
// Security (DDR-028):
//   - Origin-verify middleware blocks direct API Gateway access (CloudFront-only)
//   - Input validation on sessionId (UUID), filename (safe chars), S3 key (uuid/filename)
//   - Content-type allowlist and file size limits for uploads
//   - Cryptographically random job IDs prevent enumeration
//   - Session ownership enforced on triage results/confirm
//
// Endpoints:
//
//	GET  /api/health               — health check (no auth required)
//	GET  /api/upload-url           — presigned S3 PUT URL for browser upload
//	POST /api/triage/start         — start triage from uploaded S3 files
//	GET  /api/triage/{id}/results  — poll triage results
//	POST /api/triage/{id}/confirm  — delete confirmed files from S3
//	POST /api/download/start       — start ZIP bundle creation for a post group (DDR-034)
//	GET  /api/download/{id}/results — poll download bundle status and URLs (DDR-034)
//	GET  /api/media/thumbnail      — generate thumbnail from S3 object
//	GET  /api/media/full           — presigned GET URL for full-resolution image
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.DecodeConfig
	_ "image/png"  // register PNG decoder for image.DecodeConfig
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/klauspost/compress/zstd"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/rs/zerolog/log"
)

// --- Input Validation (DDR-028) ---

// uuidRegex matches UUID v4 format: 8-4-4-4-12 lowercase hex with dashes.
var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// safeFilenameRegex allows alphanumeric, dots, hyphens, underscores, spaces, and parentheses.
var safeFilenameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._ ()-]{0,254}$`)

func validateSessionID(id string) error {
	if !uuidRegex.MatchString(id) {
		return fmt.Errorf("invalid sessionId: must be a UUID (e.g., a1b2c3d4-e5f6-7890-abcd-ef1234567890)")
	}
	return nil
}

func validateFilename(name string) error {
	if name == "" {
		return fmt.Errorf("filename is required")
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("filename contains invalid characters")
	}
	if !safeFilenameRegex.MatchString(name) {
		return fmt.Errorf("filename contains invalid characters; only alphanumeric, dots, hyphens, underscores, spaces, and parentheses allowed")
	}
	return nil
}

func validateS3Key(key string) error {
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("invalid key")
	}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 || !uuidRegex.MatchString(parts[0]) || parts[1] == "" {
		return fmt.Errorf("invalid key format: expected <uuid>/<filename>")
	}
	return nil
}

// --- Upload Validation (DDR-028) ---

// allowedContentTypes is the content-type allowlist for uploads.
var allowedContentTypes = map[string]bool{
	// Photos
	"image/jpeg":    true,
	"image/png":     true,
	"image/gif":     true,
	"image/webp":    true,
	"image/heic":    true,
	"image/heif":    true,
	"image/tiff":    true,
	"image/bmp":     true,
	"image/svg+xml": true,
	// RAW camera formats
	"image/x-adobe-dng":     true,
	"image/x-canon-cr2":     true,
	"image/x-canon-cr3":     true,
	"image/x-nikon-nef":     true,
	"image/x-sony-arw":      true,
	"image/x-fuji-raf":      true,
	"image/x-olympus-orf":   true,
	"image/x-panasonic-rw2": true,
	"image/x-samsung-srw":   true,
	// Videos
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/x-msvideo":  true,
	"video/x-matroska": true,
	"video/3gpp":       true,
	"video/MP2T":       true,
}

const maxPhotoSize int64 = 50 * 1024 * 1024       // 50 MB
const maxVideoSize int64 = 5 * 1024 * 1024 * 1024 // 5 GB

// zipMethodZstd is the ZIP compression method ID for Zstandard (APPNOTE 6.3.7).
// Registered in init() with zstd level 12 (SpeedBestCompression in klauspost/compress).
// Requires 2+ GB Lambda memory due to zstd encoder window size at high levels.
const zipMethodZstd uint16 = 93

func isVideoContentType(ct string) bool {
	return strings.HasPrefix(ct, "video/")
}

// AWS clients initialized at cold start.
var (
	s3Client           *s3.Client
	presigner          *s3.PresignClient
	mediaBucket        string
	originVerifySecret string // DDR-028: shared secret for CloudFront origin verification
)

func init() {
	logging.Init()

	// Register Zstandard (zstd) as a ZIP compressor at level 12 (DDR-034).
	// Level 12 maps to SpeedBestCompression in klauspost/compress — the highest
	// compression the Go library supports. This trades CPU time for smaller ZIPs.
	// Requires Lambda memory ≥ 2 GB due to zstd encoder window size.
	zip.RegisterCompressor(zipMethodZstd, func(w io.Writer) (io.WriteCloser, error) {
		return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(12)))
	})

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

	originVerifySecret = os.Getenv("ORIGIN_VERIFY_SECRET")
	if originVerifySecret == "" {
		log.Warn().Msg("ORIGIN_VERIFY_SECRET not set — origin verification disabled")
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

// withOriginVerify is middleware that rejects requests lacking the correct
// x-origin-verify header. CloudFront injects this header via a custom origin
// header, so direct API Gateway access is blocked. (DDR-028 Problem 1)
func withOriginVerify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if originVerifySecret == "" {
			// Secret not configured — allow through (dev/initial deploy)
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("x-origin-verify") != originVerifySecret {
			log.Warn().Str("path", r.URL.Path).Msg("Blocked request: missing or invalid x-origin-verify header")
			httpError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/upload-url", handleUploadURL)
	mux.HandleFunc("/api/triage/start", handleTriageStart)
	mux.HandleFunc("/api/triage/", handleTriageRoutes)
	mux.HandleFunc("/api/selection/start", handleSelectionStart)
	mux.HandleFunc("/api/selection/", handleSelectionRoutes)
	mux.HandleFunc("/api/enhance/start", handleEnhanceStart)
	mux.HandleFunc("/api/enhance/", handleEnhanceRoutes)
	mux.HandleFunc("/api/media/thumbnail", handleThumbnail)
	mux.HandleFunc("/api/media/full", handleFullImage)

	// Wrap with origin-verify middleware (DDR-028)
	handler := withOriginVerify(mux)

	adapter := httpadapter.NewV2(handler)
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
//
// Security (DDR-028):
//   - sessionId must be a valid UUID
//   - filename is sanitized and validated against safe character set
//   - contentType must be in the allowed media type list
//   - Presigned URL includes Content-Length constraint to enforce size limits
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

	// Validate sessionId is a proper UUID (DDR-028 Problem 3)
	if err := validateSessionID(sessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Sanitize and validate filename (DDR-028 Problem 4)
	filename = filepath.Base(filename) // strip directory components
	if err := validateFilename(filename); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate content type against allowlist (DDR-028 Problem 7)
	if !allowedContentTypes[contentType] {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("unsupported content type: %s", contentType))
		return
	}

	// Enforce file size limits (DDR-028 Problem 7)
	sizeLimit := maxPhotoSize
	if isVideoContentType(contentType) {
		sizeLimit = maxVideoSize
	}

	key := sessionID + "/" + filename

	result, err := presigner.PresignPutObject(context.Background(), &s3.PutObjectInput{
		Bucket:        &mediaBucket,
		Key:           &key,
		ContentType:   &contentType,
		ContentLength: aws.Int64(sizeLimit),
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
)

// newJobID generates a cryptographically random job ID to prevent
// sequential enumeration. (DDR-028 Problem 8)
func newJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal().Err(err).Msg("Failed to generate random job ID")
	}
	return "triage-" + hex.EncodeToString(b)
}

func newJob(sessionID string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	id := newJobID()
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
	// Validate sessionId is a proper UUID (DDR-028 Problem 3)
	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
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

	// Use a generic "not found" to prevent job ID enumeration (DDR-028 Problem 8)
	job := getJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
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

// GET /api/triage/{id}/results?sessionId=...
func handleTriageResults(w http.ResponseWriter, r *http.Request, job *triageJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Ownership check: the caller must provide the sessionId that started the job (DDR-028 Problem 9)
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" || sessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
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
		SessionID  string   `json:"sessionId"`
		DeleteKeys []string `json:"deleteKeys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Ownership check: the caller must provide the sessionId that started the job (DDR-028 Problem 9)
	if req.SessionID == "" || req.SessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
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

	// Validate S3 key format (DDR-028 Problem 5)
	if err := validateS3Key(key); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check for pre-generated thumbnail (DDR-030): keys under /thumbnails/ are
	// already JPEG thumbnails — serve directly from S3 without regeneration.
	parts := strings.SplitN(key, "/", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[1], "thumbnails/") {
		result, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{
			Bucket: &mediaBucket,
			Key:    &key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Pre-generated thumbnail not found")
			httpError(w, http.StatusNotFound, "thumbnail not found")
			return
		}
		defer result.Body.Close()

		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		io.Copy(w, result.Body)
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

	// For videos, return a placeholder SVG (pre-generated thumbnails are preferred; DDR-030).
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

	// Validate S3 key format (DDR-028 Problem 5)
	if err := validateS3Key(key); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
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

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}

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

// --- Selection Job Management (DDR-030) ---

type selectionJob struct {
	mu          sync.Mutex
	id          string
	sessionID   string
	status      string // "pending", "processing", "complete", "error"
	selected    []selectionResultItem
	excluded    []selectionExcludedItem
	sceneGroups []selectionSceneGroup
	errMsg      string
}

type selectionResultItem struct {
	Rank           int    `json:"rank"`
	Media          int    `json:"media"`
	Filename       string `json:"filename"`
	Key            string `json:"key"`
	Type           string `json:"type"`
	Scene          string `json:"scene"`
	Justification  string `json:"justification"`
	ComparisonNote string `json:"comparisonNote,omitempty"`
	ThumbnailURL   string `json:"thumbnailUrl"`
}

type selectionExcludedItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Key          string `json:"key"`
	Reason       string `json:"reason"`
	Category     string `json:"category"`
	DuplicateOf  string `json:"duplicateOf,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

type selectionSceneGroup struct {
	Name      string                    `json:"name"`
	GPS       string                    `json:"gps,omitempty"`
	TimeRange string                    `json:"timeRange,omitempty"`
	Items     []selectionSceneGroupItem `json:"items"`
}

type selectionSceneGroupItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Key          string `json:"key"`
	Type         string `json:"type"`
	Selected     bool   `json:"selected"`
	Description  string `json:"description"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

var (
	selJobsMu sync.Mutex
	selJobs   = make(map[string]*selectionJob)
)

func newSelectionJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal().Err(err).Msg("Failed to generate random selection job ID")
	}
	return "sel-" + hex.EncodeToString(b)
}

func newSelectionJob(sessionID string) *selectionJob {
	selJobsMu.Lock()
	defer selJobsMu.Unlock()
	id := newSelectionJobID()
	j := &selectionJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	selJobs[id] = j
	return j
}

func getSelectionJob(id string) *selectionJob {
	selJobsMu.Lock()
	defer selJobsMu.Unlock()
	return selJobs[id]
}

func setSelectionJobError(job *selectionJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Selection job failed")
}

// --- Selection Endpoints (DDR-030) ---

// POST /api/selection/start
// Body: {"sessionId": "uuid", "tripContext": "...", "model": "optional-model-name"}
func handleSelectionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID   string `json:"sessionId"`
		TripContext string `json:"tripContext"`
		Model       string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" {
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	model := chat.DefaultModelName
	if req.Model != "" {
		model = req.Model
	}

	job := newSelectionJob(req.SessionID)
	go runSelectionJob(job, req.TripContext, model)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handleSelectionRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/selection/"), "/")
	if len(parts) < 2 {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	jobID := parts[0]
	if !strings.HasPrefix(jobID, "sel-") {
		jobID = "sel-" + jobID
	}
	action := parts[1]

	job := getSelectionJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleSelectionResults(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/selection/{id}/results?sessionId=...
func handleSelectionResults(w http.ResponseWriter, r *http.Request, job *selectionJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Ownership check (DDR-028)
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" || sessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":          job.id,
		"status":      job.status,
		"selected":    job.selected,
		"excluded":    job.excluded,
		"sceneGroups": job.sceneGroups,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}

// --- Selection Processing ---

func runSelectionJob(job *selectionJob, tripContext string, model string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setSelectionJobError(job, "GEMINI_API_KEY not configured")
		return
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setSelectionJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}

	// List objects in the session prefix
	prefix := job.sessionID + "/"
	listResult, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &mediaBucket,
		Prefix: &prefix,
	})
	if err != nil {
		setSelectionJobError(job, fmt.Sprintf("Failed to list S3 objects: %v", err))
		return
	}

	if len(listResult.Contents) == 0 {
		setSelectionJobError(job, "No files found for session")
		return
	}

	log.Info().Int("count", len(listResult.Contents)).Str("session", job.sessionID).Msg("Found S3 objects for selection")

	// Download each file and create MediaFile objects
	tmpDir := filepath.Join(os.TempDir(), "selection", job.sessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string

	// Filter to only media files under the session root (exclude thumbnails/ subfolder)
	for _, obj := range listResult.Contents {
		key := *obj.Key
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		// Skip files in thumbnails/ subfolder
		relPath := strings.TrimPrefix(key, prefix)
		if strings.Contains(relPath, "/") {
			log.Debug().Str("key", key).Msg("Skipping non-root-level file")
			continue
		}

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
		setSelectionJobError(job, "No supported media files found in the uploaded session")
		return
	}

	log.Info().Int("count", len(allMediaFiles)).Msg("Starting thumbnail pre-generation and selection")

	// Pre-generate and cache thumbnails in S3 (DDR-030)
	preGenerateThumbnails(ctx, job.sessionID, allMediaFiles, s3Keys)

	// Call Gemini for structured JSON selection (DDR-030)
	selResult, err := chat.AskMediaSelectionJSON(ctx, client, allMediaFiles, tripContext, model)
	if err != nil {
		setSelectionJobError(job, fmt.Sprintf("Selection failed: %v", err))
		return
	}

	// Map results to items with S3 keys and thumbnail URLs
	job.mu.Lock()
	for _, sel := range selResult.Selected {
		idx := sel.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID, strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		job.selected = append(job.selected, selectionResultItem{
			Rank:           sel.Rank,
			Media:          sel.Media,
			Filename:       sel.Filename,
			Key:            key,
			Type:           sel.Type,
			Scene:          sel.Scene,
			Justification:  sel.Justification,
			ComparisonNote: sel.ComparisonNote,
			ThumbnailURL:   fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
		})
	}
	for _, exc := range selResult.Excluded {
		idx := exc.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID, strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		job.excluded = append(job.excluded, selectionExcludedItem{
			Media:        exc.Media,
			Filename:     exc.Filename,
			Key:          key,
			Reason:       exc.Reason,
			Category:     exc.Category,
			DuplicateOf:  exc.DuplicateOf,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
		})
	}
	for _, sg := range selResult.SceneGroups {
		group := selectionSceneGroup{
			Name:      sg.Name,
			GPS:       sg.GPS,
			TimeRange: sg.TimeRange,
		}
		for _, item := range sg.Items {
			idx := item.Media - 1
			if idx < 0 || idx >= len(allMediaFiles) {
				continue
			}
			key := s3Keys[idx]
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID, strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
			group.Items = append(group.Items, selectionSceneGroupItem{
				Media:        item.Media,
				Filename:     item.Filename,
				Key:          key,
				Type:         item.Type,
				Selected:     item.Selected,
				Description:  item.Description,
				ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
			})
		}
		job.sceneGroups = append(job.sceneGroups, group)
	}
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("selected", len(job.selected)).
		Int("excluded", len(job.excluded)).
		Int("scenes", len(job.sceneGroups)).
		Msg("Selection job complete")
}

// preGenerateThumbnails generates thumbnails for all media files and uploads them to S3.
// Thumbnails are stored at {sessionId}/thumbnails/{filename}.jpg for fast serving.
// Uses goroutines for parallel generation. See DDR-030.
func preGenerateThumbnails(ctx context.Context, sessionID string, files []*filehandler.MediaFile, s3Keys []string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Max 10 concurrent thumbnail uploads

	for i, mf := range files {
		wg.Add(1)
		go func(idx int, mediaFile *filehandler.MediaFile, key string) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			filename := filepath.Base(key)
			baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", sessionID, baseName)

			// Generate thumbnail (400px for caching — frontend display size)
			thumbData, _, err := filehandler.GenerateThumbnail(mediaFile, 400)
			if err != nil {
				log.Warn().Err(err).Str("file", filename).Msg("Failed to generate thumbnail for S3 cache")
				return
			}

			// Upload to S3
			contentType := "image/jpeg"
			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &thumbKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &contentType,
			})
			if err != nil {
				log.Warn().Err(err).Str("key", thumbKey).Msg("Failed to upload thumbnail to S3")
				return
			}

			log.Debug().
				Str("file", filename).
				Str("thumbKey", thumbKey).
				Int("size", len(thumbData)).
				Msg("Thumbnail cached in S3")
		}(i, mf, s3Keys[i])
	}

	wg.Wait()
	log.Info().Int("count", len(files)).Msg("Thumbnail pre-generation complete")
}

// --- Enhancement Job Management (DDR-031) ---

type enhancementJob struct {
	mu             sync.Mutex
	id             string
	sessionID      string
	status         string // "pending", "processing", "complete", "error"
	items          []enhancementResultItem
	totalCount     int
	completedCount int
	errMsg         string
}

type enhancementResultItem struct {
	Key              string               `json:"key"`
	Filename         string               `json:"filename"`
	Phase            string               `json:"phase"`
	OriginalKey      string               `json:"originalKey"`
	EnhancedKey      string               `json:"enhancedKey"`
	OriginalThumbKey string               `json:"originalThumbKey"`
	EnhancedThumbKey string               `json:"enhancedThumbKey"`
	Phase1Text       string               `json:"phase1Text"`
	Analysis         *chat.AnalysisResult `json:"analysis,omitempty"`
	ImagenEdits      int                  `json:"imagenEdits"`
	FeedbackHistory  []chat.FeedbackEntry `json:"feedbackHistory"`
	Error            string               `json:"error,omitempty"`
}

var (
	enhJobsMu sync.Mutex
	enhJobs   = make(map[string]*enhancementJob)
)

func newEnhancementJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal().Err(err).Msg("Failed to generate random enhancement job ID")
	}
	return "enh-" + hex.EncodeToString(b)
}

func newEnhancementJob(sessionID string) *enhancementJob {
	enhJobsMu.Lock()
	defer enhJobsMu.Unlock()
	id := newEnhancementJobID()
	j := &enhancementJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	enhJobs[id] = j
	return j
}

func getEnhancementJob(id string) *enhancementJob {
	enhJobsMu.Lock()
	defer enhJobsMu.Unlock()
	return enhJobs[id]
}

func setEnhancementJobError(job *enhancementJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Enhancement job failed")
}

// --- Enhancement Endpoints (DDR-031) ---

// POST /api/enhance/start
// Body: {"sessionId": "uuid", "keys": ["uuid/file1.jpg", ...]}
func handleEnhanceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string   `json:"sessionId"`
		Keys      []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" {
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Keys) == 0 {
		httpError(w, http.StatusBadRequest, "at least one key is required")
		return
	}

	// Validate all keys belong to the session
	for _, key := range req.Keys {
		if err := validateS3Key(key); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", err.Error()))
			return
		}
		if !strings.HasPrefix(key, req.SessionID+"/") {
			httpError(w, http.StatusBadRequest, "key does not belong to session")
			return
		}
	}

	// Filter to photos only (enhancement is for photos)
	var photoKeys []string
	for _, key := range req.Keys {
		ext := strings.ToLower(filepath.Ext(key))
		if filehandler.IsImage(ext) {
			photoKeys = append(photoKeys, key)
		}
	}

	if len(photoKeys) == 0 {
		httpError(w, http.StatusBadRequest, "no photo files in the provided keys")
		return
	}

	job := newEnhancementJob(req.SessionID)
	go runEnhancementJob(job, photoKeys)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handleEnhanceRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/enhance/"), "/")
	if len(parts) < 2 {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	jobID := parts[0]
	if !strings.HasPrefix(jobID, "enh-") {
		jobID = "enh-" + jobID
	}
	action := parts[1]

	job := getEnhancementJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleEnhanceResults(w, r, job)
	case "feedback":
		handleEnhanceFeedback(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/enhance/{id}/results?sessionId=...
func handleEnhanceResults(w http.ResponseWriter, r *http.Request, job *enhancementJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Ownership check (DDR-028)
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" || sessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":             job.id,
		"status":         job.status,
		"items":          job.items,
		"totalCount":     job.totalCount,
		"completedCount": job.completedCount,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}

// POST /api/enhance/{id}/feedback
// Body: {"sessionId": "uuid", "key": "uuid/file.jpg", "feedback": "make it brighter"}
func handleEnhanceFeedback(w http.ResponseWriter, r *http.Request, job *enhancementJob) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Key       string `json:"key"`
		Feedback  string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Ownership check (DDR-028)
	if req.SessionID == "" || req.SessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	if req.Key == "" || req.Feedback == "" {
		httpError(w, http.StatusBadRequest, "key and feedback are required")
		return
	}

	// Find the item
	job.mu.Lock()
	var targetIdx int = -1
	for i, item := range job.items {
		if item.Key == req.Key || item.EnhancedKey == req.Key {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		job.mu.Unlock()
		httpError(w, http.StatusNotFound, "item not found in enhancement job")
		return
	}
	item := job.items[targetIdx]
	job.mu.Unlock()

	// Run feedback processing asynchronously
	go func() {
		ctx := context.Background()
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			return
		}

		genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create Gemini client for feedback")
			return
		}
		geminiImageClient := chat.NewGeminiImageClient(genaiClient)

		// Download the current enhanced image
		enhancedKey := item.EnhancedKey
		if enhancedKey == "" {
			enhancedKey = item.Key
		}

		tmpPath, cleanup, err := downloadFromS3(ctx, enhancedKey)
		if err != nil {
			log.Error().Err(err).Str("key", enhancedKey).Msg("Failed to download enhanced image for feedback")
			return
		}
		defer cleanup()

		imageData, err := os.ReadFile(tmpPath)
		if err != nil {
			log.Error().Err(err).Msg("Failed to read enhanced image")
			return
		}

		// Determine MIME type
		ext := strings.ToLower(filepath.Ext(enhancedKey))
		mime := "image/jpeg"
		if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
			mime = m
		}

		// Get image dimensions for mask generation
		imgConfig, _, err := image.DecodeConfig(bytes.NewReader(imageData))
		imageWidth := 1024
		imageHeight := 1024
		if err == nil {
			imageWidth = imgConfig.Width
			imageHeight = imgConfig.Height
		}

		// Set up Imagen client (optional)
		var imagenClient *chat.ImagenClient
		vertexProject := os.Getenv("VERTEX_AI_PROJECT")
		vertexRegion := os.Getenv("VERTEX_AI_REGION")
		vertexToken := os.Getenv("VERTEX_AI_TOKEN")
		if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
			imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
		}

		// Process feedback
		resultData, resultMIME, feedbackEntry, err := chat.ProcessFeedback(
			ctx, geminiImageClient, imagenClient,
			imageData, mime, req.Feedback,
			item.FeedbackHistory, imageWidth, imageHeight,
		)
		if err != nil {
			log.Warn().Err(err).Msg("Feedback processing failed")
		}

		// Upload the result to S3
		if resultData != nil && len(resultData) > 0 {
			feedbackKey := fmt.Sprintf("%s/enhanced/%s", job.sessionID, filepath.Base(item.Key))
			contentType := resultMIME
			_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &feedbackKey,
				Body:        bytes.NewReader(resultData),
				ContentType: &contentType,
			})
			if uploadErr != nil {
				log.Error().Err(uploadErr).Str("key", feedbackKey).Msg("Failed to upload feedback result")
				return
			}

			// Generate and upload thumbnail
			thumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", job.sessionID,
				strings.TrimSuffix(filepath.Base(item.Key), filepath.Ext(item.Key)))
			thumbData, _, thumbErr := generateThumbnailFromBytes(resultData, resultMIME, 400)
			if thumbErr == nil {
				thumbContentType := "image/jpeg"
				s3Client.PutObject(ctx, &s3.PutObjectInput{
					Bucket:      &mediaBucket,
					Key:         &thumbKey,
					Body:        bytes.NewReader(thumbData),
					ContentType: &thumbContentType,
				})
			}

			// Update job state
			job.mu.Lock()
			job.items[targetIdx].EnhancedKey = feedbackKey
			job.items[targetIdx].EnhancedThumbKey = thumbKey
			job.items[targetIdx].Phase = chat.PhaseFeedback
			if feedbackEntry != nil {
				job.items[targetIdx].FeedbackHistory = append(job.items[targetIdx].FeedbackHistory, *feedbackEntry)
			}
			job.mu.Unlock()
		}
	}()

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "processing",
	})
}

// --- Enhancement Processing ---

func runEnhancementJob(job *enhancementJob, photoKeys []string) {
	job.mu.Lock()
	job.status = "processing"
	job.totalCount = len(photoKeys)
	// Initialize items
	for _, key := range photoKeys {
		job.items = append(job.items, enhancementResultItem{
			Key:         key,
			Filename:    filepath.Base(key),
			Phase:       chat.PhaseInitial,
			OriginalKey: key,
		})
	}
	job.mu.Unlock()

	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setEnhancementJobError(job, "GEMINI_API_KEY not configured")
		return
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setEnhancementJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}
	geminiImageClient := chat.NewGeminiImageClient(genaiClient)

	// Set up Imagen client (optional — only if Vertex AI is configured)
	var imagenClient *chat.ImagenClient
	vertexProject := os.Getenv("VERTEX_AI_PROJECT")
	vertexRegion := os.Getenv("VERTEX_AI_REGION")
	vertexToken := os.Getenv("VERTEX_AI_TOKEN")
	if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
		imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
		log.Info().Msg("Imagen 3 client configured for Phase 3 surgical edits")
	} else {
		log.Info().Msg("Imagen 3 not configured — Phase 3 will be skipped")
	}

	// Process each photo sequentially (to stay within rate limits)
	// Future: use Step Functions Map state for parallel processing
	for i, key := range photoKeys {
		log.Info().
			Int("index", i+1).
			Int("total", len(photoKeys)).
			Str("key", key).
			Msg("Enhancing photo")

		// Download from S3
		tmpPath, cleanup, err := downloadFromS3(ctx, key)
		if err != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = fmt.Sprintf("Download failed: %v", err)
			job.mu.Unlock()
			log.Warn().Err(err).Str("key", key).Msg("Failed to download photo for enhancement")
			continue
		}

		imageData, err := os.ReadFile(tmpPath)
		cleanup()
		if err != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = fmt.Sprintf("Read failed: %v", err)
			job.mu.Unlock()
			continue
		}

		// Determine MIME type
		ext := strings.ToLower(filepath.Ext(key))
		mime := "image/jpeg"
		if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
			mime = m
		}

		// Get image dimensions for mask generation
		imgConfig, _, configErr := image.DecodeConfig(bytes.NewReader(imageData))
		imageWidth := 1024
		imageHeight := 1024
		if configErr == nil {
			imageWidth = imgConfig.Width
			imageHeight = imgConfig.Height
		}

		// Run the full enhancement pipeline
		job.mu.Lock()
		job.items[i].Phase = chat.PhaseOne
		job.mu.Unlock()

		state, err := chat.RunFullEnhancement(ctx, geminiImageClient, imagenClient, imageData, mime, imageWidth, imageHeight)
		if err != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = err.Error()
			if state != nil {
				job.items[i].Phase1Text = state.Phase1Text
				job.items[i].Analysis = state.Analysis
			}
			job.mu.Unlock()
			log.Warn().Err(err).Str("key", key).Msg("Enhancement pipeline failed")
			// Continue with next photo — partial success is acceptable
			job.mu.Lock()
			job.completedCount++
			job.mu.Unlock()
			continue
		}

		// Upload enhanced image to S3
		enhancedKey := fmt.Sprintf("%s/enhanced/%s", job.sessionID, filepath.Base(key))
		contentType := state.CurrentMIME
		if contentType == "" {
			contentType = mime
		}
		_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      &mediaBucket,
			Key:         &enhancedKey,
			Body:        bytes.NewReader(state.CurrentData),
			ContentType: &contentType,
		})
		if uploadErr != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = fmt.Sprintf("Upload failed: %v", uploadErr)
			job.mu.Unlock()
			log.Error().Err(uploadErr).Str("key", enhancedKey).Msg("Failed to upload enhanced image")
			job.mu.Lock()
			job.completedCount++
			job.mu.Unlock()
			continue
		}

		// Generate and upload thumbnail of enhanced version
		enhancedThumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", job.sessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		thumbData, _, thumbErr := generateThumbnailFromBytes(state.CurrentData, contentType, 400)
		if thumbErr == nil {
			thumbContentType := "image/jpeg"
			s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &enhancedThumbKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &thumbContentType,
			})
		}

		// Update job state
		job.mu.Lock()
		job.items[i].Phase = state.Phase
		job.items[i].EnhancedKey = enhancedKey
		job.items[i].EnhancedThumbKey = enhancedThumbKey
		job.items[i].OriginalThumbKey = fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		job.items[i].Phase1Text = state.Phase1Text
		job.items[i].Analysis = state.Analysis
		job.items[i].ImagenEdits = state.ImagenEdits
		job.completedCount++
		job.mu.Unlock()

		log.Info().
			Int("index", i+1).
			Str("key", key).
			Str("phase", state.Phase).
			Float64("score", 0). // Will be filled from analysis
			Msg("Photo enhancement complete")
	}

	job.mu.Lock()
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("total", job.totalCount).
		Int("completed", job.completedCount).
		Msg("Enhancement job complete")
}

// --- Download Job Management (DDR-034) ---

// maxVideoZipBytes is the maximum size of a single video ZIP bundle.
// Calculated as 30 seconds × 100 Mbps ÷ 8 = 375 MB.
// Based on AT&T Internet Air typical download speed in San Jose (~100 Mbps).
const maxVideoZipBytes int64 = 375 * 1024 * 1024

type downloadJob struct {
	mu        sync.Mutex
	id        string
	sessionID string
	status    string // "pending", "processing", "complete", "error"
	bundles   []downloadBundle
	errMsg    string
}

type downloadBundle struct {
	Type        string `json:"type"`                  // "images" or "videos"
	Name        string `json:"name"`                  // display name: "images.zip" or "videos-1.zip"
	ZipKey      string `json:"zipKey"`                // S3 key of the created ZIP
	DownloadURL string `json:"downloadUrl,omitempty"` // presigned GET URL (populated on complete)
	FileCount   int    `json:"fileCount"`
	TotalSize   int64  `json:"totalSize"` // total original file size in bytes
	ZipSize     int64  `json:"zipSize"`   // ZIP file size in bytes (populated on complete)
	Status      string `json:"status"`    // "pending", "processing", "complete", "error"
	Error       string `json:"error,omitempty"`
}

var (
	dlJobsMu sync.Mutex
	dlJobs   = make(map[string]*downloadJob)
)

func newDownloadJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal().Err(err).Msg("Failed to generate random download job ID")
	}
	return "dl-" + hex.EncodeToString(b)
}

func newDownloadJob(sessionID string) *downloadJob {
	dlJobsMu.Lock()
	defer dlJobsMu.Unlock()
	id := newDownloadJobID()
	j := &downloadJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	dlJobs[id] = j
	return j
}

func getDownloadJob(id string) *downloadJob {
	dlJobsMu.Lock()
	defer dlJobsMu.Unlock()
	return dlJobs[id]
}

func setDownloadJobError(job *downloadJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Download job failed")
}

// --- Download Endpoints (DDR-034) ---

// POST /api/download/start
// Body: {"sessionId": "uuid", "keys": ["uuid/enhanced/file1.jpg", ...], "groupLabel": "Tokyo Day 1"}
func handleDownloadStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID  string   `json:"sessionId"`
		Keys       []string `json:"keys"`
		GroupLabel string   `json:"groupLabel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" {
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Keys) == 0 {
		httpError(w, http.StatusBadRequest, "at least one key is required")
		return
	}

	// Validate all keys belong to the session
	for _, key := range req.Keys {
		if err := validateS3Key(key); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", err.Error()))
			return
		}
		if !strings.HasPrefix(key, req.SessionID+"/") {
			httpError(w, http.StatusBadRequest, "key does not belong to session")
			return
		}
	}

	job := newDownloadJob(req.SessionID)
	go runDownloadJob(job, req.Keys, req.GroupLabel)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handleDownloadRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/download/"), "/")
	if len(parts) < 2 {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	jobID := parts[0]
	if !strings.HasPrefix(jobID, "dl-") {
		jobID = "dl-" + jobID
	}
	action := parts[1]

	job := getDownloadJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleDownloadResults(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/download/{id}/results?sessionId=...
func handleDownloadResults(w http.ResponseWriter, r *http.Request, job *downloadJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Ownership check (DDR-028)
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" || sessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":      job.id,
		"status":  job.status,
		"bundles": job.bundles,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}

// --- Download Processing (DDR-034) ---

// fileWithSize holds an S3 key and its object size (from HeadObject).
type fileWithSize struct {
	key  string
	size int64
}

// runDownloadJob creates ZIP bundles for the given media keys.
// Strategy: 1 ZIP for all images, videos split into bundles ≤ 375 MB each.
func runDownloadJob(job *downloadJob, keys []string, groupLabel string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	// Step 1: Query file sizes via HeadObject and separate images from videos
	var images []fileWithSize
	var videos []fileWithSize

	for _, key := range keys {
		headResult, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: &mediaBucket,
			Key:    &key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("HeadObject failed, skipping file")
			continue
		}

		size := *headResult.ContentLength
		ext := strings.ToLower(filepath.Ext(key))

		if filehandler.IsVideo(ext) {
			videos = append(videos, fileWithSize{key: key, size: size})
		} else {
			images = append(images, fileWithSize{key: key, size: size})
		}
	}

	if len(images) == 0 && len(videos) == 0 {
		setDownloadJobError(job, "No downloadable files found")
		return
	}

	// Step 2: Plan bundles
	var bundles []downloadBundle

	// All images go into one ZIP
	if len(images) > 0 {
		var totalSize int64
		for _, img := range images {
			totalSize += img.size
		}
		bundles = append(bundles, downloadBundle{
			Type:      "images",
			Name:      sanitizeZipName(groupLabel, "images", 0),
			FileCount: len(images),
			TotalSize: totalSize,
			Status:    "pending",
		})
	}

	// Videos grouped into bundles ≤ maxVideoZipBytes (375 MB)
	if len(videos) > 0 {
		videoGroups := groupVideosBySize(videos, maxVideoZipBytes)
		for i, group := range videoGroups {
			var totalSize int64
			for _, v := range group {
				totalSize += v.size
			}
			bundles = append(bundles, downloadBundle{
				Type:      "videos",
				Name:      sanitizeZipName(groupLabel, "videos", i+1),
				FileCount: len(group),
				TotalSize: totalSize,
				Status:    "pending",
			})
		}
	}

	// Store initial bundle plan
	job.mu.Lock()
	job.bundles = bundles
	job.mu.Unlock()

	log.Info().
		Int("images", len(images)).
		Int("videos", len(videos)).
		Int("bundles", len(bundles)).
		Str("job", job.id).
		Msg("Download bundle plan created")

	// Step 3: Create each ZIP bundle
	// Track which video group index we're on
	videoGroupIdx := 0
	videoGroups := groupVideosBySize(videos, maxVideoZipBytes)

	for i := range bundles {
		job.mu.Lock()
		job.bundles[i].Status = "processing"
		job.mu.Unlock()

		var filesToZip []fileWithSize
		if bundles[i].Type == "images" {
			filesToZip = images
		} else {
			filesToZip = videoGroups[videoGroupIdx]
			videoGroupIdx++
		}

		zipKey := fmt.Sprintf("%s/downloads/%s/%s", job.sessionID, job.id, bundles[i].Name)

		zipSize, err := createZipBundle(ctx, filesToZip, zipKey)
		if err != nil {
			job.mu.Lock()
			job.bundles[i].Status = "error"
			job.bundles[i].Error = err.Error()
			job.mu.Unlock()
			log.Error().Err(err).Str("bundle", bundles[i].Name).Msg("Failed to create ZIP bundle")
			continue
		}

		// Generate presigned download URL (1 hour expiry)
		downloadResult, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket:                     &mediaBucket,
			Key:                        &zipKey,
			ResponseContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="%s"`, bundles[i].Name)),
		}, s3.WithPresignExpires(1*time.Hour))
		if err != nil {
			job.mu.Lock()
			job.bundles[i].Status = "error"
			job.bundles[i].Error = "failed to generate download URL"
			job.mu.Unlock()
			log.Error().Err(err).Str("key", zipKey).Msg("Failed to generate presigned GET URL for ZIP")
			continue
		}

		job.mu.Lock()
		job.bundles[i].ZipKey = zipKey
		job.bundles[i].ZipSize = zipSize
		job.bundles[i].DownloadURL = downloadResult.URL
		job.bundles[i].Status = "complete"
		job.mu.Unlock()

		log.Info().
			Str("bundle", bundles[i].Name).
			Int64("zipSize", zipSize).
			Int("files", len(filesToZip)).
			Msg("ZIP bundle created")
	}

	// Mark job complete
	job.mu.Lock()
	allComplete := true
	for _, b := range job.bundles {
		if b.Status != "complete" && b.Status != "error" {
			allComplete = false
			break
		}
	}
	if allComplete {
		job.status = "complete"
	}
	job.mu.Unlock()

	log.Info().
		Str("job", job.id).
		Int("bundles", len(bundles)).
		Msg("Download job complete")
}

// groupVideosBySize groups videos into bundles where each bundle's total size ≤ maxBytes.
// Videos larger than maxBytes get their own bundle.
// Uses a first-fit-decreasing bin packing heuristic for better packing.
func groupVideosBySize(videos []fileWithSize, maxBytes int64) [][]fileWithSize {
	if len(videos) == 0 {
		return nil
	}

	// Sort videos by size descending (first-fit-decreasing)
	sorted := make([]fileWithSize, len(videos))
	copy(sorted, videos)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].size > sorted[j].size
	})

	var groups [][]fileWithSize
	groupSizes := []int64{}

	for _, video := range sorted {
		placed := false

		// If the video itself exceeds maxBytes, it gets its own group
		if video.size > maxBytes {
			groups = append(groups, []fileWithSize{video})
			groupSizes = append(groupSizes, video.size)
			continue
		}

		// Try to fit into an existing group
		for i, currentSize := range groupSizes {
			if currentSize+video.size <= maxBytes {
				groups[i] = append(groups[i], video)
				groupSizes[i] += video.size
				placed = true
				break
			}
		}

		// If it doesn't fit anywhere, create a new group
		if !placed {
			groups = append(groups, []fileWithSize{video})
			groupSizes = append(groupSizes, video.size)
		}
	}

	return groups
}

// createZipBundle downloads files from S3, creates a zstd-compressed ZIP in /tmp,
// and uploads it to S3. Uses Zstandard level 12 compression (DDR-034).
// Returns the size of the created ZIP file.
func createZipBundle(ctx context.Context, files []fileWithSize, zipKey string) (int64, error) {
	// Create temp file for the ZIP
	tmpFile, err := os.CreateTemp("", "download-*.zip")
	if err != nil {
		return 0, fmt.Errorf("create temp ZIP: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Create ZIP writer
	zipWriter := zip.NewWriter(tmpFile)

	for _, file := range files {
		filename := filepath.Base(file.key)

		// Download file from S3
		getResult, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: &mediaBucket,
			Key:    &file.key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", file.key).Msg("Failed to download file for ZIP, skipping")
			continue
		}

		// Create ZIP entry
		header := &zip.FileHeader{
			Name:   filename,
			Method: zipMethodZstd, // Zstandard level 12 compression (DDR-034)
		}
		header.SetModTime(time.Now())

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			getResult.Body.Close()
			return 0, fmt.Errorf("create ZIP entry for %s: %w", filename, err)
		}

		// Stream from S3 response directly into ZIP
		if _, err := io.Copy(writer, getResult.Body); err != nil {
			getResult.Body.Close()
			return 0, fmt.Errorf("write to ZIP for %s: %w", filename, err)
		}
		getResult.Body.Close()
	}

	if err := zipWriter.Close(); err != nil {
		tmpFile.Close()
		return 0, fmt.Errorf("close ZIP writer: %w", err)
	}
	tmpFile.Close()

	// Get ZIP file size
	info, err := os.Stat(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("stat ZIP file: %w", err)
	}
	zipSize := info.Size()

	// Upload ZIP to S3
	zipFile, err := os.Open(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("open ZIP for upload: %w", err)
	}
	defer zipFile.Close()

	contentType := "application/zip"
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &mediaBucket,
		Key:         &zipKey,
		Body:        zipFile,
		ContentType: &contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("upload ZIP to S3: %w", err)
	}

	return zipSize, nil
}

// sanitizeZipName creates a ZIP filename from the group label and bundle type.
func sanitizeZipName(groupLabel, bundleType string, index int) string {
	// Clean the group label for use in filenames
	name := groupLabel
	if name == "" {
		name = "media"
	}

	// Replace unsafe characters
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return '-'
	}, name)
	name = strings.TrimSpace(name)

	// Truncate to reasonable length
	if len(name) > 50 {
		name = name[:50]
	}

	if bundleType == "images" {
		return fmt.Sprintf("%s-images.zip", name)
	}
	return fmt.Sprintf("%s-videos-%d.zip", name, index)
}

// generateThumbnailFromBytes creates a thumbnail from raw image bytes.
func generateThumbnailFromBytes(imageData []byte, mimeType string, maxDimension int) ([]byte, string, error) {
	// Write to temp file, generate thumbnail, clean up
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

// httpError sends a JSON error response. The clientMsg is returned to the caller.
// Optional internalDetails are logged server-side but never sent to the client.
// This prevents leaking sensitive info (S3 paths, ARNs, stack traces) while
// keeping client messages useful for debugging. (DDR-028 Problem 16)
func httpError(w http.ResponseWriter, status int, clientMsg string, internalDetails ...string) {
	if len(internalDetails) > 0 {
		log.Error().
			Int("status", status).
			Str("clientMsg", clientMsg).
			Strs("internalDetails", internalDetails).
			Msg("HTTP error with internal details")
	}
	respondJSON(w, status, map[string]string{"error": clientMsg})
}

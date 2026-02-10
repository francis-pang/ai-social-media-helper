package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/rs/zerolog/log"
)

// --- S3 Multipart Upload (DDR-054) ---
//
// Three endpoints enable browser-initiated S3 multipart uploads for large files:
//   - POST /api/upload-multipart/init     — create multipart upload + batch-presign all part URLs
//   - POST /api/upload-multipart/complete  — complete multipart upload with ETags
//   - POST /api/upload-multipart/abort     — abort multipart upload (cleanup on failure)

const (
	// minPartSize is the minimum S3 multipart part size (5 MB).
	minPartSize int64 = 5 * 1024 * 1024
	// maxPartSize is the maximum allowed chunk size from the client (100 MB).
	maxPartSize int64 = 100 * 1024 * 1024
	// maxParts is the S3 maximum number of parts in a multipart upload.
	maxParts int64 = 10000
	// presignExpiry is how long each presigned part URL is valid.
	presignExpiry = 60 * time.Minute
)

// --- Init ---

type multipartInitRequest struct {
	SessionID   string `json:"sessionId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	FileSize    int64  `json:"fileSize"`
	ChunkSize   int64  `json:"chunkSize"`
}

type partURL struct {
	PartNumber int32  `json:"partNumber"`
	URL        string `json:"url"`
}

type multipartInitResponse struct {
	UploadID string    `json:"uploadId"`
	Key      string    `json:"key"`
	PartURLs []partURL `json:"partUrls"`
}

// POST /api/upload-multipart/init
//
// Creates an S3 multipart upload and batch-presigns all UploadPart URLs.
// This eliminates per-part round trips from the browser.
//
// Security: reuses existing validators for sessionId, filename, and contentType (DDR-028).
func handleMultipartInit(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleMultipartInit")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req multipartInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// --- Input validation (DDR-028) ---

	if req.SessionID == "" || req.Filename == "" || req.ContentType == "" {
		httpError(w, http.StatusBadRequest, "sessionId, filename, and contentType are required")
		return
	}

	if err := validateSessionID(req.SessionID); err != nil {
		log.Warn().Err(err).Str("sessionId", req.SessionID).Msg("Session ID validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.Filename = filepath.Base(req.Filename)
	if err := validateFilename(req.Filename); err != nil {
		log.Warn().Err(err).Str("filename", req.Filename).Msg("Filename validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !allowedContentTypes[req.ContentType] {
		log.Warn().Str("contentType", req.ContentType).Msg("Unsupported content type")
		httpError(w, http.StatusBadRequest, fmt.Sprintf("unsupported content type: %s", req.ContentType))
		return
	}

	// Validate file size against limits.
	if req.FileSize <= 0 {
		httpError(w, http.StatusBadRequest, "fileSize must be positive")
		return
	}
	maxSize := maxPhotoSize
	if isVideoContentType(req.ContentType) {
		maxSize = maxVideoSize
	}
	if req.FileSize > maxSize {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("file too large: %d bytes (max %d)", req.FileSize, maxSize))
		return
	}

	// Validate chunk size.
	if req.ChunkSize < minPartSize {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("chunkSize must be at least %d bytes (5 MB)", minPartSize))
		return
	}
	if req.ChunkSize > maxPartSize {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("chunkSize must be at most %d bytes (100 MB)", maxPartSize))
		return
	}

	numParts := int64(math.Ceil(float64(req.FileSize) / float64(req.ChunkSize)))
	if numParts > maxParts {
		httpError(w, http.StatusBadRequest, fmt.Sprintf("too many parts: %d (max %d); increase chunkSize", numParts, maxParts))
		return
	}

	key := req.SessionID + "/" + req.Filename

	log.Info().
		Str("sessionId", req.SessionID).
		Str("filename", req.Filename).
		Str("contentType", req.ContentType).
		Int64("fileSize", req.FileSize).
		Int64("chunkSize", req.ChunkSize).
		Int64("numParts", numParts).
		Msg("Creating multipart upload (DDR-054)")

	// Create the multipart upload.
	createResult, err := s3Client.CreateMultipartUpload(context.Background(), &s3.CreateMultipartUploadInput{
		Bucket:      &mediaBucket,
		Key:         &key,
		ContentType: &req.ContentType,
	})
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("Failed to create multipart upload")
		httpError(w, http.StatusInternalServerError, "failed to create multipart upload")
		return
	}

	uploadID := *createResult.UploadId

	// Batch-presign all UploadPart URLs.
	partURLs := make([]partURL, 0, numParts)
	for i := int32(1); i <= int32(numParts); i++ {
		partNum := i
		presignResult, err := presigner.PresignUploadPart(context.Background(), &s3.UploadPartInput{
			Bucket:     &mediaBucket,
			Key:        &key,
			UploadId:   &uploadID,
			PartNumber: &partNum,
		}, s3.WithPresignExpires(presignExpiry))
		if err != nil {
			log.Error().Err(err).Str("key", key).Int32("partNumber", partNum).Msg("Failed to presign upload part")
			// Attempt to abort the multipart upload to avoid orphaned state.
			_, _ = s3Client.AbortMultipartUpload(context.Background(), &s3.AbortMultipartUploadInput{
				Bucket:   &mediaBucket,
				Key:      &key,
				UploadId: &uploadID,
			})
			httpError(w, http.StatusInternalServerError, "failed to presign upload parts")
			return
		}
		partURLs = append(partURLs, partURL{
			PartNumber: partNum,
			URL:        presignResult.URL,
		})
	}

	log.Info().
		Str("uploadId", uploadID).
		Str("key", key).
		Int("parts", len(partURLs)).
		Msg("Multipart upload created with presigned part URLs")

	respondJSON(w, http.StatusOK, multipartInitResponse{
		UploadID: uploadID,
		Key:      key,
		PartURLs: partURLs,
	})
}

// --- Complete ---

type completePart struct {
	PartNumber int32  `json:"partNumber"`
	ETag       string `json:"etag"`
}

type multipartCompleteRequest struct {
	SessionID string         `json:"sessionId"`
	Key       string         `json:"key"`
	UploadID  string         `json:"uploadId"`
	Parts     []completePart `json:"parts"`
}

type multipartCompleteResponse struct {
	Key string `json:"key"`
}

// POST /api/upload-multipart/complete
//
// Completes an S3 multipart upload by assembling parts.
func handleMultipartComplete(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleMultipartComplete")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req multipartCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.SessionID == "" || req.Key == "" || req.UploadID == "" || len(req.Parts) == 0 {
		httpError(w, http.StatusBadRequest, "sessionId, key, uploadId, and parts are required")
		return
	}

	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := validateS3Key(req.Key); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Build S3 CompletedMultipartUpload parts list.
	s3Parts := make([]s3types.CompletedPart, len(req.Parts))
	for i, p := range req.Parts {
		partNum := p.PartNumber
		etag := p.ETag
		s3Parts[i] = s3types.CompletedPart{
			PartNumber: &partNum,
			ETag:       &etag,
		}
	}

	log.Info().
		Str("sessionId", req.SessionID).
		Str("key", req.Key).
		Str("uploadId", req.UploadID).
		Int("parts", len(req.Parts)).
		Msg("Completing multipart upload (DDR-054)")

	_, err := s3Client.CompleteMultipartUpload(context.Background(), &s3.CompleteMultipartUploadInput{
		Bucket:   &mediaBucket,
		Key:      &req.Key,
		UploadId: &req.UploadID,
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: s3Parts,
		},
	})
	if err != nil {
		log.Error().Err(err).Str("key", req.Key).Str("uploadId", req.UploadID).Msg("Failed to complete multipart upload")
		httpError(w, http.StatusInternalServerError, "failed to complete multipart upload")
		return
	}

	log.Info().Str("key", req.Key).Str("uploadId", req.UploadID).Msg("Multipart upload completed successfully")

	respondJSON(w, http.StatusOK, multipartCompleteResponse{
		Key: req.Key,
	})
}

// --- Abort ---

type multipartAbortRequest struct {
	SessionID string `json:"sessionId"`
	Key       string `json:"key"`
	UploadID  string `json:"uploadId"`
}

// POST /api/upload-multipart/abort
//
// Aborts an in-progress S3 multipart upload, cleaning up uploaded parts
// to avoid orphaned storage costs.
func handleMultipartAbort(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleMultipartAbort")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req multipartAbortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.SessionID == "" || req.Key == "" || req.UploadID == "" {
		httpError(w, http.StatusBadRequest, "sessionId, key, and uploadId are required")
		return
	}

	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := validateS3Key(req.Key); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	log.Info().
		Str("sessionId", req.SessionID).
		Str("key", req.Key).
		Str("uploadId", req.UploadID).
		Msg("Aborting multipart upload (DDR-054)")

	_, err := s3Client.AbortMultipartUpload(context.Background(), &s3.AbortMultipartUploadInput{
		Bucket:   &mediaBucket,
		Key:      &req.Key,
		UploadId: &req.UploadID,
	})
	if err != nil {
		log.Error().Err(err).Str("key", req.Key).Str("uploadId", req.UploadID).Msg("Failed to abort multipart upload")
		httpError(w, http.StatusInternalServerError, "failed to abort multipart upload")
		return
	}

	log.Info().Str("key", req.Key).Str("uploadId", req.UploadID).Msg("Multipart upload aborted")

	respondJSON(w, http.StatusOK, map[string]string{"status": "aborted"})
}

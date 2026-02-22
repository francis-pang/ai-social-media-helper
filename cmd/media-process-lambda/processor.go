package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/fpang/gemini-media-cli/internal/s3util"
	"github.com/fpang/gemini-media-cli/internal/store"
)

func processFile(ctx context.Context, key string) error {
	fileStart := time.Now()

	// Parse sessionId from key: {sessionId}/{filename}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		log.Debug().Str("key", key).Msg("Skipping key: not in {sessionId}/{filename} format")
		return nil
	}
	sessionID := parts[0]
	remainder := parts[1]

	// Filter: skip our own output directories
	if strings.Contains(remainder, "/") {
		// Key has subdirectory: thumbnails/, processed/, compressed/
		log.Debug().Str("key", key).Msg("Skipping key: subdirectory (generated artifact)")
		return nil
	}
	filename := remainder

	log.Info().Str("key", key).Str("sessionId", sessionID).Str("filename", filename).Msg("Processing file")

	// Tag the browser-uploaded object for cost allocation (DDR-049).
	// Presigned PUT URLs cannot embed tags, so we apply them on first access.
	if err := s3util.TagObject(ctx, s3Client, mediaBucket, key); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("Failed to tag uploaded object (non-fatal)")
	}

	// Validate extension
	ext := strings.ToLower(filepath.Ext(filename))
	if !filehandler.IsSupported(ext) {
		log.Warn().Str("key", key).Str("ext", ext).Msg("Unsupported file extension")
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Unsupported file extension: %s", ext))
	}

	isImage := filehandler.IsImage(ext)
	isVideo := filehandler.IsVideo(ext)
	fileType := "image"
	if isVideo {
		fileType = "video"
	}

	// Head object to get size and content type
	headResult, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	})
	if err != nil {
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Failed to read file metadata: %v", err))
	}

	fileSize := *headResult.ContentLength
	contentType := ""
	if headResult.ContentType != nil {
		contentType = *headResult.ContentType
	}
	mimeType, _ := filehandler.GetMIMEType(ext)
	if mimeType == "" {
		mimeType = contentType
	}

	log.Debug().Str("key", key).Int64("size", fileSize).Str("contentType", contentType).Str("mimeType", mimeType).Msg("File metadata retrieved")

	// Download file to /tmp
	tmpDir := filepath.Join(os.TempDir(), "media-process", sessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	localPath := filepath.Join(tmpDir, filename)
	if err := s3util.DownloadToFile(ctx, s3Client, mediaBucket, key, localPath); err != nil {
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Failed to download file: %v", err))
	}

	// DDR-067: Check for duplicate content via fingerprint before processing
	jobID, err := findTriageJobID(ctx, sessionID)
	if err != nil {
		log.Warn().Err(err).Str("sessionId", sessionID).Msg("Could not find triage job ID for dedup check")
		jobID = ""
	}
	if jobID != "" && fileProcessStore != nil {
		fp, fpErr := computeFingerprint(localPath, fileSize)
		if fpErr == nil {
			existingFilename, lookupErr := fileProcessStore.GetFingerprintMapping(ctx, sessionID, jobID, fp)
			if lookupErr == nil && existingFilename != "" {
				log.Info().Str("key", key).Str("fingerprint", fp).Str("originalFile", existingFilename).Msg("Duplicate content detected — copying existing result (DDR-067)")
				copyErr := copyExistingResult(ctx, sessionID, jobID, existingFilename, filename, key)
				if copyErr == nil {
					return nil
				}
				log.Warn().Err(copyErr).Msg("Failed to copy existing result, processing normally")
			}
		}
	}

	// Load media file (extracts metadata)
	mf, err := filehandler.LoadMediaFile(localPath)
	if err != nil {
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Failed to load media file: %v", err))
	}

	// Extract metadata as string map for DDB storage
	metadataMap := make(map[string]string)
	if mf.Metadata != nil {
		metadataMap["mediaType"] = mf.Metadata.GetMediaType()
		if mf.Metadata.HasGPSData() {
			lat, lon := mf.Metadata.GetGPS()
			metadataMap["gpsLat"] = fmt.Sprintf("%.6f", lat)
			metadataMap["gpsLon"] = fmt.Sprintf("%.6f", lon)
		}
		if mf.Metadata.HasDateData() {
			metadataMap["date"] = mf.Metadata.GetDate().Format(time.RFC3339)
		}
	}

	// Determine processing strategy
	var processedKey string
	var thumbnailKey string
	converted := false

	if isImage {
		// Generate thumbnail (always)
		thumbData, _, err := filehandler.GenerateThumbnail(mf, thumbnailPx)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to generate thumbnail")
		} else {
			baseName := strings.TrimSuffix(filename, ext)
			thumbnailKey = fmt.Sprintf("%s/thumbnails/%s.jpg", sessionID, baseName)
			thumbContentType := "image/jpeg"
			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &thumbnailKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &thumbContentType,
				Tagging:     s3util.ProjectTagging(),
			})
			if err != nil {
				log.Warn().Err(err).Str("thumbnailKey", thumbnailKey).Msg("Failed to upload thumbnail")
				thumbnailKey = ""
			} else {
				log.Debug().Str("thumbnailKey", thumbnailKey).Int("size", len(thumbData)).Msg("Thumbnail uploaded")
			}
		}

		// Small photo: skip conversion, use original
		processedKey = key
		// Note: Image conversion (resize large photos) can be added later
		// For now, all images use the original and just get a thumbnail

	} else if isVideo {
		// Generate video thumbnail
		thumbData, _, err := filehandler.GenerateThumbnail(mf, thumbnailPx)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to generate video thumbnail")
		} else {
			baseName := strings.TrimSuffix(filename, ext)
			thumbnailKey = fmt.Sprintf("%s/thumbnails/%s.jpg", sessionID, baseName)
			thumbContentType := "image/jpeg"
			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &thumbnailKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &thumbContentType,
				Tagging:     s3util.ProjectTagging(),
			})
			if err != nil {
				log.Warn().Err(err).Str("thumbnailKey", thumbnailKey).Msg("Failed to upload video thumbnail")
				thumbnailKey = ""
			}
		}

		// Video compression for Gemini (reuse existing CompressVideoForGemini)
		// Use a shorter deadline than the Lambda timeout so we always have time
		// to write file results and increment processedCount even if compression
		// is too slow for large videos.
		if filehandler.IsFFmpegAvailable() {
			var videoMeta *filehandler.VideoMetadata
			if mf.Metadata != nil {
				if vm, ok := mf.Metadata.(*filehandler.VideoMetadata); ok {
					videoMeta = vm
				}
			}

			// DDR-067: Log selected preset for observability
			if videoMeta != nil && videoMeta.Duration > 0 {
				preset := filehandler.SelectPreset(videoMeta.Duration)
				log.Info().Int("preset", preset).Dur("duration", videoMeta.Duration).Str("key", key).Msg("Adaptive preset selected (DDR-067)")
			}

			compressCtx, compressCancel := context.WithTimeout(ctx, 12*time.Minute) // DDR-067: increased from 8 min
			compressedPath, _, cleanup, err := filehandler.CompressVideoForGemini(compressCtx, localPath, videoMeta)
			compressCancel()
			if err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Video compression failed — using original")
				processedKey = key
			} else {
				defer cleanup()
				// Upload compressed video
				baseName := strings.TrimSuffix(filename, ext)
				processedKey = fmt.Sprintf("%s/processed/%s.webm", sessionID, baseName)
				compressedFile, err := os.Open(compressedPath)
				if err != nil {
					log.Warn().Err(err).Msg("Failed to open compressed video")
					processedKey = key
				} else {
					compressedContentType := "video/webm"
					_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
						Bucket:      &mediaBucket,
						Key:         &processedKey,
						Body:        compressedFile,
						ContentType: &compressedContentType,
						Tagging:     s3util.ProjectTagging(),
					})
					compressedFile.Close()
					if err != nil {
						log.Warn().Err(err).Str("processedKey", processedKey).Msg("Failed to upload compressed video")
						processedKey = key
					} else {
						converted = true
						log.Info().Str("processedKey", processedKey).Msg("Compressed video uploaded")
					}
				}
				os.Remove(compressedPath)
			}
		} else {
			log.Debug().Str("key", key).Msg("ffmpeg not available — using original video")
			processedKey = key
		}
	}

	// DDR-067: jobID was already resolved at the top for dedup; re-resolve if empty
	if jobID == "" {
		jobID, err = findTriageJobID(ctx, sessionID)
		if err != nil {
			log.Warn().Err(err).Str("sessionId", sessionID).Msg("Could not find triage job ID — file result will be orphaned")
			jobID = ""
		}
	}

	// Compute fingerprint for dedup records (DDR-067)
	fingerprint := ""
	if fp, fpErr := computeFingerprint(localPath, fileSize); fpErr == nil {
		fingerprint = fp
	}

	// Write result to file-processing table
	result := &store.FileResult{
		Filename:     filename,
		Status:       "valid",
		OriginalKey:  key,
		ProcessedKey: processedKey,
		ThumbnailKey: thumbnailKey,
		FileType:     fileType,
		MimeType:     mimeType,
		FileSize:     fileSize,
		Converted:    converted,
		Fingerprint:  fingerprint,
		Metadata:     metadataMap,
	}

	if jobID != "" {
		if err := fileProcessStore.PutFileResult(ctx, sessionID, jobID, result); err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to write file result to DDB")
		}

		// DDR-067: Store fingerprint mapping for dedup
		if fingerprint != "" && fileProcessStore != nil {
			if err := fileProcessStore.PutFingerprintMapping(ctx, sessionID, jobID, fingerprint, filename); err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Failed to store fingerprint mapping (non-fatal)")
			}
		}

		// Increment processedCount on the TriageJob
		newCount, err := sessionStore.IncrementTriageProcessedCount(ctx, sessionID, jobID)
		if err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to increment processedCount")
		} else {
			log.Debug().Str("key", key).Int("processedCount", newCount).Msg("processedCount incremented")
		}
	}

	processingMs := time.Since(fileStart).Milliseconds()
	log.Info().
		Str("key", key).
		Str("fileType", fileType).
		Bool("converted", converted).
		Int64("processingMs", processingMs).
		Msg("File processing complete")

	// Emit EMF metrics
	metrics.New("AiSocialMedia").
		Dimension("Operation", "mediaProcess").
		Dimension("FileType", fileType).
		Metric("FileProcessingMs", float64(processingMs), metrics.UnitMilliseconds).
		Metric("FileSize", float64(fileSize), metrics.UnitBytes).
		Count("FilesProcessed").
		Property("sessionId", sessionID).
		Property("filename", filename).
		Property("converted", converted).
		Flush()

	return nil
}

// computeFingerprint creates a content fingerprint: SHA-256(fileSize || first64KB || last64KB).
// Matches the browser-side quickFingerprint algorithm (DDR-067).
func computeFingerprint(filePath string, fileSize int64) (string, error) {
	const chunkSize = 64 * 1024

	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if err := binary.Write(h, binary.BigEndian, float64(fileSize)); err != nil {
		return "", err
	}

	buf := make([]byte, chunkSize)
	n, _ := io.ReadFull(f, buf)
	h.Write(buf[:n])

	if fileSize > int64(chunkSize) {
		if _, err := f.Seek(-int64(chunkSize), io.SeekEnd); err == nil {
			n, _ = io.ReadFull(f, buf)
			h.Write(buf[:n])
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyExistingResult copies an existing FileResult for a duplicate file (DDR-067).
// Writes a new result pointing to the same processedKey/thumbnailKey and increments processedCount.
func copyExistingResult(ctx context.Context, sessionID, jobID, originalFilename, newFilename, newKey string) error {
	if fileProcessStore == nil {
		return fmt.Errorf("file process store not available")
	}

	original, err := fileProcessStore.GetFileResultByFilename(ctx, sessionID, jobID, originalFilename)
	if err != nil || original == nil {
		return fmt.Errorf("original result not found: %w", err)
	}

	result := &store.FileResult{
		Filename:     newFilename,
		Status:       original.Status,
		OriginalKey:  newKey,
		ProcessedKey: original.ProcessedKey,
		ThumbnailKey: original.ThumbnailKey,
		FileType:     original.FileType,
		MimeType:     original.MimeType,
		FileSize:     original.FileSize,
		Converted:    original.Converted,
		Fingerprint:  original.Fingerprint,
		Metadata:     original.Metadata,
	}

	if err := fileProcessStore.PutFileResult(ctx, sessionID, jobID, result); err != nil {
		return fmt.Errorf("failed to write dedup result: %w", err)
	}

	newCount, err := sessionStore.IncrementTriageProcessedCount(ctx, sessionID, jobID)
	if err != nil {
		log.Warn().Err(err).Str("key", newKey).Msg("Failed to increment processedCount for dedup copy")
	} else {
		log.Debug().Str("key", newKey).Int("processedCount", newCount).Msg("processedCount incremented (dedup copy)")
	}

	metrics.New("AiSocialMedia").
		Dimension("Operation", "mediaProcess").
		Count("DuplicateFilesSkipped").
		Property("sessionId", sessionID).
		Property("filename", newFilename).
		Property("originalFilename", originalFilename).
		Flush()

	return nil
}

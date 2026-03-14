package batch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/genai"

	"github.com/fpang/ai-social-media-helper/internal/httputil"
	"github.com/fpang/ai-social-media-helper/internal/media"
	"github.com/fpang/ai-social-media-helper/internal/store"
	"github.com/fpang/ai-social-media-helper/internal/s3util"
)

// BuildMediaParts builds genai parts for a batch using pre-uploaded GCS URIs for videos.
// Images are fetched via S3 presigned URLs; videos use the provided GCS URIs.
func BuildMediaParts(ctx context.Context, sessionID string, meta BatchMeta, gcsURIs map[int]string, deps SubmitDeps) ([]*genai.Part, error) {
	var parts []*genai.Part
	fileResultMap := make(map[string]store.FileResult)
	if deps.FileProcessStore != nil {
		results, err := deps.FileProcessStore.GetSessionFileResults(ctx, sessionID)
		if err == nil {
			for _, fr := range results {
				fileResultMap[fr.Filename] = fr
			}
		}
	}

	for i, item := range meta.MediaItems {
		ext := strings.ToLower(filepath.Ext(item.S3Key))
		filename := filepath.Base(item.S3Key)
		fr, hasFileResult := fileResultMap[filename]

		if item.MediaType == "image" || media.IsImage(ext) {
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
			} else if deps.S3Client != nil && deps.PresignClient != nil {
				keyParts := strings.SplitN(item.S3Key, "/", 2)
				thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", keyParts[0], strings.TrimSuffix(filename, ext))
				processedKey := fmt.Sprintf("%s/processed/%s%s", keyParts[0], strings.TrimSuffix(filename, ext), ".webp")
				if head, _ := deps.S3Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &deps.MediaBucket, Key: &processedKey}); head != nil {
					useKey = processedKey
					mimeType = "image/webp"
				} else if head, _ := deps.S3Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &deps.MediaBucket, Key: &thumbKey}); head != nil {
					useKey = thumbKey
				}
			}
			url, err := s3util.GeneratePresignedURL(ctx, deps.PresignClient, deps.MediaBucket, useKey, 15*time.Minute)
			if err != nil {
				return nil, fmt.Errorf("image %s: %w", useKey, err)
			}
			imgData, err := httputil.FetchURLToBytes(ctx, url)
			if err != nil {
				return nil, fmt.Errorf("image %s: %w", useKey, err)
			}
			parts = append(parts, &genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: imgData}})
		} else {
			gsURI := gcsURIs[i]
			if gsURI == "" {
				return nil, fmt.Errorf("video item %d: missing GCS URI", i)
			}
			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{MIMEType: "video/webm", FileURI: gsURI},
			})
		}
	}
	return parts, nil
}

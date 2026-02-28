package mcpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

type MediaConfig struct {
	Items        []MediaItem
	S3Presigner  S3Presigner
	MediaBucket  string
	GeminiClient *genai.Client
}

type MediaItem struct {
	Index        int
	Filename     string
	FileType     string
	MIMEType     string
	S3Key        string
	ThumbnailKey string
	Size         int64
	Metadata     map[string]string
}

type S3Presigner interface {
	GeneratePresignedURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
}

const maxPresignedVideoBytes int64 = 20 * 1024 * 1024

func registerMediaTools(server *mcp.Server, mediaCfg *MediaConfig) {
	type fetchMediaArgs struct {
		MediaIndices []int `json:"media_indices" jsonschema:"1-based indices of media items to fetch for visual inspection"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "fetch_media",
		Description: "Fetch the actual media content (photos/videos) for specific items by index. Call this for items where you cannot confidently determine saveability from metadata alone. Returns thumbnails for images and video references for videos.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args fetchMediaArgs) (*mcp.CallToolResult, any, error) {
		var content []mcp.Content
		var fetchedCount int

		for _, idx := range args.MediaIndices {
			if idx < 1 || idx > len(mediaCfg.Items) {
				content = append(content, &mcp.TextContent{
					Text: fmt.Sprintf("Media %d: invalid index (valid range: 1-%d)", idx, len(mediaCfg.Items)),
				})
				continue
			}

			item := mediaCfg.Items[idx-1]

			switch item.FileType {
			case "image":
				imgContent, err := fetchImageThumbnail(ctx, mediaCfg, item)
				if err != nil {
					content = append(content, &mcp.TextContent{
						Text: fmt.Sprintf("Media %d (%s): fetch error: %v", idx, item.Filename, err),
					})
					continue
				}
				content = append(content, imgContent)
				fetchedCount++

			case "video":
				videoContent, err := fetchVideo(ctx, mediaCfg, item)
				if err != nil {
					content = append(content, &mcp.TextContent{
						Text: fmt.Sprintf("Media %d (%s): fetch error: %v", idx, item.Filename, err),
					})
					continue
				}
				content = append(content, videoContent...)
				fetchedCount++
			}
		}

		content = append(content, &mcp.TextContent{
			Text: fmt.Sprintf("Fetched %d of %d requested media items.", fetchedCount, len(args.MediaIndices)),
		})

		return &mcp.CallToolResult{Content: content}, nil, nil
	})
}

func fetchImageThumbnail(ctx context.Context, cfg *MediaConfig, item MediaItem) (mcp.Content, error) {
	key := item.ThumbnailKey
	if key == "" {
		key = item.S3Key
	}
	url, err := cfg.S3Presigner.GeneratePresignedURL(ctx, cfg.MediaBucket, key, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	data, err := downloadBytes(ctx, url)
	if err != nil {
		return nil, err
	}
	return &mcp.ImageContent{
		Data:     data,
		MIMEType: item.MIMEType,
	}, nil
}

func fetchVideo(ctx context.Context, cfg *MediaConfig, item MediaItem) ([]mcp.Content, error) {
	url, err := cfg.S3Presigner.GeneratePresignedURL(ctx, cfg.MediaBucket, item.S3Key, 15*time.Minute)
	if err != nil {
		return nil, err
	}

	if item.Size > 0 && item.Size <= maxPresignedVideoBytes {
		return []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf(`{"_media_ref": "video", "uri": %q, "mime_type": %q, "filename": %q}`,
				url, item.MIMEType, item.Filename),
		}}, nil
	}

	if cfg.GeminiClient == nil {
		return nil, fmt.Errorf("Gemini client required for videos > %d MiB", maxPresignedVideoBytes/(1024*1024))
	}

	tmpPath, cleanup, err := downloadToTempFile(ctx, url)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	file, err := uploadAndWait(ctx, cfg.GeminiClient, tmpPath, item.MIMEType)
	if err != nil {
		return nil, err
	}

	return []mcp.Content{&mcp.TextContent{
		Text: fmt.Sprintf(`{"_media_ref": "video", "uri": %q, "mime_type": %q, "filename": %q}`,
			file.URI, file.MIMEType, item.Filename),
	}}, nil
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func downloadToTempFile(ctx context.Context, url string) (string, func(), error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}
	tmpFile, err := os.CreateTemp("", "mcp-media-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()
	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

func uploadAndWait(ctx context.Context, client *genai.Client, path, mimeType string) (*genai.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	file, err := client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: mimeType,
	})
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}

	const pollInterval = 5 * time.Second
	const pollTimeout = 5 * time.Minute
	deadline := time.Now().Add(pollTimeout)

	for file.State == genai.FileStateProcessing {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for Gemini file processing after %v", pollTimeout)
		}
		time.Sleep(pollInterval)
		file, err = client.Files.Get(ctx, file.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("get file state: %w", err)
		}
	}

	if file.State == genai.FileStateFailed {
		return nil, fmt.Errorf("Gemini file processing failed: %s", file.Name)
	}

	log.Debug().Str("name", file.Name).Str("uri", file.URI).Msg("Video uploaded to Gemini Files API via MCP")
	return file, nil
}

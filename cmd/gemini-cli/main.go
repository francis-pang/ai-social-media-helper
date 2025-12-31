package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/auth"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
)

// Hardcoded media paths for development iterations
const (
	// Iteration 7: Single image upload
	hardcodedImagePath = "/Users/fpang/OneDrive - Adobe/Francis-Document/20251230_092113.heic"

	// Iteration 9: Single video upload
	hardcodedVideoPath = "/Users/fpang/OneDrive - Adobe/Francis-Document/20251230_171931.mp4"
)

func main() {
	logging.Init()

	apiKey, err := auth.GetAPIKey()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to retrieve API key")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create Gemini client")
	}
	defer client.Close()

	log.Info().Msg("connection successful - Gemini client initialized")

	// Validate API key by making a test API call
	if err := auth.ValidateAPIKey(ctx, client); err != nil {
		handleValidationError(err)
	}

	log.Info().Msg("API key validation complete - ready for operations")

	// Iteration 9: Single video upload with hardcoded path
	// (Iteration 8 - Image directory - skipped for now)
	runMediaAnalysis(ctx, client, hardcodedVideoPath)
}

// runMediaAnalysis loads a media file (image or video) and generates a social media post description.
func runMediaAnalysis(ctx context.Context, client *genai.Client, mediaPath string) {
	ext := strings.ToLower(filepath.Ext(mediaPath))
	isVideo := filehandler.IsVideo(ext)
	isImage := filehandler.IsImage(ext)

	mediaType := "media"
	emoji := "📁"
	if isVideo {
		mediaType = "video"
		emoji = "🎬"
	} else if isImage {
		mediaType = "image"
		emoji = "📸"
	}

	log.Info().Str("path", mediaPath).Str("type", mediaType).Msg("Starting media analysis")

	// Load the media file (extracts metadata, determines if Files API needed)
	mediaFile, err := filehandler.LoadMediaFile(mediaPath)
	if err != nil {
		log.Fatal().Err(err).Str("path", mediaPath).Msg("failed to load media file")
	}

	// Display header
	fmt.Println()
	fmt.Println("============================================")
	fmt.Printf("%s Analyzing %s for Social Media Post\n", emoji, strings.Title(mediaType))
	fmt.Println("============================================")
	fmt.Printf("File: %s\n", filepath.Base(mediaPath))
	fmt.Printf("Size: %.2f MB\n", float64(mediaFile.Size)/(1024*1024))
	fmt.Printf("Type: %s\n", mediaFile.MIMEType)
	fmt.Println("Upload: Files API")

	// Display extracted metadata
	if mediaFile.Metadata != nil {
		fmt.Println("--------------------------------------------")
		displayMetadata(mediaFile.Metadata)
	} else {
		fmt.Println("--------------------------------------------")
		fmt.Println("⚠️  No metadata could be extracted")
	}

	fmt.Println("--------------------------------------------")
	fmt.Printf("⏳ Uploading %s to Gemini Files API...\n", mediaType)
	fmt.Println()

	// Build the appropriate prompt based on media type
	prompt := chat.BuildSocialMediaPrompt(mediaFile.Metadata)
	log.Debug().Str("prompt_length", fmt.Sprintf("%d chars", len(prompt))).Msg("Using social media prompt")

	response, err := chat.AskMediaQuestion(ctx, client, mediaFile, prompt)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to analyze media")
	}

	fmt.Println("✅ Analysis Complete!")
	fmt.Println("============================================")
	fmt.Println()
	fmt.Println(response)
}

// displayMetadata prints the extracted metadata to the console.
func displayMetadata(metadata filehandler.MediaMetadata) {
	switch m := metadata.(type) {
	case *filehandler.ImageMetadata:
		displayImageMetadata(m)
	case *filehandler.VideoMetadata:
		displayVideoMetadata(m)
	default:
		fmt.Println("📋 Metadata extracted (unknown type)")
	}
}

// displayImageMetadata prints image-specific metadata.
func displayImageMetadata(m *filehandler.ImageMetadata) {
	fmt.Println("📍 EXIF Metadata Extracted:")
	if m.HasGPS {
		fmt.Printf("   GPS: %.6f, %.6f\n", m.Latitude, m.Longitude)
		fmt.Printf("   Map: https://www.google.com/maps?q=%.6f,%.6f\n", m.Latitude, m.Longitude)
	}
	if m.HasDate {
		fmt.Printf("   Date: %s\n", m.DateTaken.Format("Monday, January 2, 2006 at 3:04 PM"))
	}
	if m.CameraMake != "" || m.CameraModel != "" {
		fmt.Printf("   Camera: %s %s\n", m.CameraMake, m.CameraModel)
	}
}

// displayVideoMetadata prints video-specific metadata.
func displayVideoMetadata(m *filehandler.VideoMetadata) {
	fmt.Println("🎥 Video Metadata Extracted:")
	if m.HasGPS {
		fmt.Printf("   GPS: %.6f, %.6f\n", m.Latitude, m.Longitude)
		fmt.Printf("   Map: https://www.google.com/maps?q=%.6f,%.6f\n", m.Latitude, m.Longitude)
	}
	if m.HasDate {
		fmt.Printf("   Date: %s\n", m.CreateDate.Format("Monday, January 2, 2006 at 3:04 PM"))
	}
	if m.Duration > 0 {
		fmt.Printf("   Duration: %s\n", formatDuration(m.Duration))
	}
	if m.Width > 0 && m.Height > 0 {
		resolution := fmt.Sprintf("%dx%d", m.Width, m.Height)
		if m.Width >= 3840 {
			resolution += " (4K UHD)"
		} else if m.Width >= 1920 {
			resolution += " (Full HD)"
		}
		fmt.Printf("   Resolution: %s\n", resolution)
	}
	if m.FrameRate > 0 {
		fmt.Printf("   Frame Rate: %.2f fps\n", m.FrameRate)
	}
	if m.Codec != "" {
		fmt.Printf("   Codec: %s\n", m.Codec)
	}
	if m.BitRate > 0 {
		fmt.Printf("   Bit Rate: %.2f Mbps\n", float64(m.BitRate)/(1024*1024))
	}
}

// formatDuration formats a time.Duration in a human-readable format.
func formatDuration(d interface{}) string {
	switch v := d.(type) {
	case int:
		minutes := v / 60
		seconds := v % 60
		if minutes > 0 {
			return fmt.Sprintf("%d:%02d", minutes, seconds)
		}
		return fmt.Sprintf("0:%02d", seconds)
	default:
		// Handle time.Duration
		if dur, ok := d.(interface{ Seconds() float64 }); ok {
			totalSeconds := int(dur.Seconds())
			minutes := totalSeconds / 60
			seconds := totalSeconds % 60
			if minutes > 0 {
				return fmt.Sprintf("%d:%02d", minutes, seconds)
			}
			return fmt.Sprintf("0:%02d", seconds)
		}
		return fmt.Sprintf("%v", d)
	}
}

// handleValidationError processes validation errors and exits with appropriate messaging.
func handleValidationError(err error) {
	var validationErr *auth.ValidationError
	if errors.As(err, &validationErr) {
		switch validationErr.Type {
		case auth.ErrTypeNoKey:
			log.Fatal().Msg("No API key configured. Set GEMINI_API_KEY or run scripts/setup-gpg-credentials.sh")
		case auth.ErrTypeInvalidKey:
			log.Fatal().Err(err).Msg("Invalid API key. Please check your API key and try again")
		case auth.ErrTypeNetworkError:
			log.Fatal().Err(err).Msg("Network error. Please check your internet connection")
		case auth.ErrTypeQuotaExceeded:
			log.Fatal().Err(err).Msg("API quota exceeded. Please try again later or check your usage limits")
		default:
			log.Fatal().Err(err).Msg("API key validation failed")
		}
	} else {
		log.Fatal().Err(err).Msg("unexpected error during API key validation")
	}
	os.Exit(1)
}

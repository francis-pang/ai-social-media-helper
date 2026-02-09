package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/cli"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"google.golang.org/genai"
)

// CLI flags
var (
	directoryFlag string
	maxDepthFlag  int
	limitFlag     int
	contextFlag   string
	modelFlag     string
)

// rootCmd is the main Cobra command for the CLI.
var rootCmd = &cobra.Command{
	Use:   "media-select",
	Short: "AI-powered photo selection for social media",
	Long: `Media Select analyzes photos and videos in a directory and uses AI to select
the most representative media items for an Instagram post.

The tool scans the specified directory (recursively by default), extracts
metadata from images and videos, compresses videos for efficient upload,
and asks Gemini to rank and select the best media for social media.

Examples:
  media-select --directory /path/to/photos --context "Weekend trip to Kyoto"
  media-select -d ./vacation-photos -c "Birthday party at restaurant then karaoke"
  media-select -d ./photos --max-depth 2 --limit 50
  media-select -d ./media --model gemini-3-pro-preview
  media-select  # Interactive mode - prompts for directory and context`,
	Run: runMain,
}

func init() {
	rootCmd.Flags().StringVarP(&directoryFlag, "directory", "d", "", "Directory containing media to analyze")
	rootCmd.Flags().IntVar(&maxDepthFlag, "max-depth", 0, "Maximum recursion depth (0 = unlimited)")
	rootCmd.Flags().IntVar(&limitFlag, "limit", 0, "Maximum media items to process (0 = unlimited)")
	rootCmd.Flags().StringVarP(&contextFlag, "context", "c", "", "Trip/event description for media selection (e.g., 'Birthday party at restaurant then karaoke')")
	rootCmd.Flags().StringVarP(&modelFlag, "model", "m", chat.DefaultModelName, "Gemini model to use (e.g., gemini-3-flash-preview, gemini-3-pro-preview)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runMain is the main execution logic called by Cobra.
func runMain(cmd *cobra.Command, args []string) {
	logging.Init()

	// Determine and validate directory path
	dirPath := directoryFlag
	if dirPath == "" {
		dirPath = cli.PromptForDirectory()
	}
	dirPath = cli.ValidateAndResolveDirectory(dirPath)

	// Initialize Gemini client
	ctx, client := cli.InitGeminiClient()

	// Get trip context
	tripContext := contextFlag
	if tripContext == "" {
		tripContext = promptForContext()
	}

	// Run directory selection with options and context
	runDirectorySelection(ctx, client, dirPath, tripContext)
}

// promptForContext prompts the user interactively for trip/event description.
// Returns empty string if the user enters nothing (context is optional but recommended).
func promptForContext() string {
	fmt.Println()
	fmt.Println("Describe your trip/event (helps Gemini select the best photos):")
	fmt.Println("Examples: 'Weekend trip to Kyoto - temples, food tour, night market'")
	fmt.Println("          'Birthday party at restaurant then karaoke'")
	fmt.Print("Context (optional): ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read context input")
		return ""
	}

	return strings.TrimSpace(input)
}

// runDirectorySelection scans a directory, processes media, and asks Gemini to select
// the most representative media items for an Instagram post using quality-agnostic criteria.
// Supports both images and videos (DDR-020: Mixed Media Selection).
func runDirectorySelection(ctx context.Context, client *genai.Client, dirPath string, tripContext string) {
	log.Info().
		Str("path", dirPath).
		Int("max_depth", maxDepthFlag).
		Int("limit", limitFlag).
		Bool("has_context", tripContext != "").
		Str("model", modelFlag).
		Msg("Starting quality-agnostic media selection")

	// Configure scan options
	opts := filehandler.ScanOptions{
		MaxDepth: maxDepthFlag,
		Limit:    limitFlag,
	}

	// Scan directory for images AND videos (mixed media)
	files, err := filehandler.ScanDirectoryMediaWithOptions(dirPath, opts)
	if err != nil {
		log.Fatal().Err(err).Str("path", dirPath).Msg("failed to scan directory")
	}

	if len(files) == 0 {
		log.Fatal().Str("path", dirPath).Msg("no supported media found in directory")
	}

	// Count media types
	var imageCount, videoCount int
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if filehandler.IsImage(ext) {
			imageCount++
		} else if filehandler.IsVideo(ext) {
			videoCount++
		}
	}

	// Display header
	fmt.Println()
	fmt.Println("============================================")
	fmt.Println("📁 Media Selection")
	fmt.Println("============================================")
	fmt.Printf("Directory: %s\n", dirPath)
	fmt.Printf("Images found: %d\n", imageCount)
	fmt.Printf("Videos found: %d\n", videoCount)
	fmt.Printf("Total media: %d\n", len(files))
	if limitFlag > 0 && len(files) == limitFlag {
		fmt.Printf("(limited to %d)\n", limitFlag)
	}
	fmt.Printf("Max selection: %d\n", chat.DefaultMaxMedia)
	fmt.Printf("Model: %s\n", modelFlag)
	if tripContext != "" {
		fmt.Printf("Context: %s\n", tripContext)
	}
	fmt.Println("--------------------------------------------")

	// Display summary of found media
	fmt.Println("📸 Media to analyze:")
	for i, file := range files {
		// Show relative path from base directory if recursive
		displayPath := filepath.Base(file.Path)
		if relPath, err := filepath.Rel(dirPath, file.Path); err == nil && relPath != displayPath {
			displayPath = relPath
		}

		sizeMB := float64(file.Size) / (1024 * 1024)
		ext := strings.ToLower(filepath.Ext(file.Path))

		// Determine media type indicator
		typeIndicator := "📷"
		durationStr := ""
		if filehandler.IsVideo(ext) {
			typeIndicator = "🎬"
			if file.Metadata != nil {
				if vm, ok := file.Metadata.(*filehandler.VideoMetadata); ok && vm.Duration > 0 {
					durationStr = fmt.Sprintf(" %s", cli.FormatDurationShort(vm.Duration))
				}
			}
		}

		metaInfo := ""
		if file.Metadata != nil {
			if file.Metadata.HasGPSData() {
				metaInfo += " 📍GPS"
			}
			if file.Metadata.HasDateData() {
				metaInfo += " 📅Date"
			}
		}

		fmt.Printf("   %2d. %s (%.1f MB) %s%s%s\n", i+1, displayPath, sizeMB, typeIndicator, durationStr, metaInfo)
	}

	fmt.Println("--------------------------------------------")

	// Show processing steps based on content
	if videoCount > 0 {
		fmt.Println("⏳ Compressing videos...")
	}
	fmt.Println("⏳ Processing media and sending to Gemini...")
	fmt.Println()

	// Ask Gemini to select media using quality-agnostic criteria
	response, err := chat.AskMediaSelection(ctx, client, files, chat.DefaultMaxMedia, tripContext, modelFlag)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get media selection from Gemini")
	}

	fmt.Println("✅ Media Selection Complete!")
	fmt.Println("============================================")
	fmt.Println()
	fmt.Println(response)
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

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	modelFlag     string
	dryRunFlag    bool
)

// rootCmd is the main Cobra command for the media-triage CLI.
var rootCmd = &cobra.Command{
	Use:   "media-triage",
	Short: "AI-powered media triage - identify unsaveable photos and videos",
	Long: `Media Triage scans a directory of photos and videos and uses AI to determine
which files are worth keeping. Media that is too dark, too blurry, accidental,
or otherwise unsaveable is flagged for deletion.

The tool sends all media to Gemini in a single batch for efficient evaluation.
Videos under 2 seconds are automatically flagged without using AI.
After displaying the triage report, you are prompted to confirm deletion.

Examples:
  media-triage --directory /path/to/photos
  media-triage -d ./vacation-photos --dry-run
  media-triage -d ./photos --max-depth 2 --limit 100
  media-triage -d ./media --model gemini-3-pro-preview
  media-triage  # Interactive mode - prompts for directory`,
	Run: runMain,
}

func init() {
	rootCmd.Flags().StringVarP(&directoryFlag, "directory", "d", "", "Directory containing media to triage")
	rootCmd.Flags().IntVar(&maxDepthFlag, "max-depth", 0, "Maximum recursion depth (0 = unlimited)")
	rootCmd.Flags().IntVar(&limitFlag, "limit", 0, "Maximum media items to process (0 = unlimited)")
	rootCmd.Flags().StringVarP(&modelFlag, "model", "m", chat.DefaultModelName, "Gemini model to use (e.g., gemini-3-flash-preview, gemini-3-pro-preview)")
	rootCmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "Show triage report without prompting for deletion")
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

	// Run triage
	runTriage(ctx, client, dirPath)
}

// runTriage scans a directory, evaluates media quality with AI, and offers to delete unsaveable files.
func runTriage(ctx context.Context, client *genai.Client, dirPath string) {
	log.Info().
		Str("path", dirPath).
		Int("max_depth", maxDepthFlag).
		Int("limit", limitFlag).
		Str("model", modelFlag).
		Msg("Starting media triage")

	// Configure scan options
	opts := filehandler.ScanOptions{
		MaxDepth: maxDepthFlag,
		Limit:    limitFlag,
	}

	// Scan directory for images AND videos
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
	fmt.Println("Media Triage")
	fmt.Println("============================================")
	fmt.Printf("Directory: %s\n", dirPath)
	fmt.Printf("Images found: %d\n", imageCount)
	fmt.Printf("Videos found: %d\n", videoCount)
	fmt.Printf("Total media: %d\n", len(files))
	if limitFlag > 0 && len(files) == limitFlag {
		fmt.Printf("(limited to %d)\n", limitFlag)
	}
	fmt.Printf("Model: %s\n", modelFlag)
	if dryRunFlag {
		fmt.Println("Mode: DRY RUN (no deletion)")
	}
	fmt.Println("--------------------------------------------")

	// Pre-filter: flag videos under 2 seconds
	var filesToAnalyze []*filehandler.MediaFile
	var preFilteredResults []chat.TriageResult
	preFilteredPaths := make(map[string]bool) // track paths for pre-filtered items

	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if filehandler.IsVideo(ext) && file.Metadata != nil {
			if vm, ok := file.Metadata.(*filehandler.VideoMetadata); ok && vm.Duration > 0 && vm.Duration < 2*time.Second {
				preFilteredResults = append(preFilteredResults, chat.TriageResult{
					Filename: filepath.Base(file.Path),
					Saveable: false,
					Reason:   fmt.Sprintf("Video too short (%.1fs) - likely accidental recording", vm.Duration.Seconds()),
				})
				preFilteredPaths[file.Path] = true
				fmt.Printf("   PRE-FILTER: %s (%.1fs) - too short, skipping AI analysis\n", filepath.Base(file.Path), vm.Duration.Seconds())
				continue
			}
		}
		filesToAnalyze = append(filesToAnalyze, file)
	}

	if len(preFilteredResults) > 0 {
		fmt.Printf("\nPre-filtered %d short video(s) without AI analysis.\n", len(preFilteredResults))
	}

	// Batch send remaining media to Gemini for triage
	var aiResults []chat.TriageResult
	if len(filesToAnalyze) > 0 {
		fmt.Println("--------------------------------------------")

		// Count remaining media types
		var aiImageCount, aiVideoCount int
		for _, file := range filesToAnalyze {
			ext := strings.ToLower(filepath.Ext(file.Path))
			if filehandler.IsImage(ext) {
				aiImageCount++
			} else if filehandler.IsVideo(ext) {
				aiVideoCount++
			}
		}
		_ = aiImageCount // used for display

		if aiVideoCount > 0 {
			fmt.Println("Compressing videos...")
		}
		fmt.Printf("Sending %d media items to Gemini for triage...\n", len(filesToAnalyze))
		fmt.Println()

		aiResults, err = chat.AskMediaTriage(ctx, client, filesToAnalyze, modelFlag)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to get triage results from Gemini")
		}
	}

	// Build complete results map: path -> TriageResult
	// Match AI results back to files by index
	type triageItem struct {
		path   string
		result chat.TriageResult
	}

	var allItems []triageItem

	// Add pre-filtered items
	for _, file := range files {
		if preFilteredPaths[file.Path] {
			for _, pr := range preFilteredResults {
				if pr.Filename == filepath.Base(file.Path) {
					allItems = append(allItems, triageItem{path: file.Path, result: pr})
					break
				}
			}
		}
	}

	// Add AI results (matched by index to filesToAnalyze)
	for i, result := range aiResults {
		if i < len(filesToAnalyze) {
			allItems = append(allItems, triageItem{path: filesToAnalyze[i].Path, result: result})
		}
	}

	// Separate into keep and discard lists
	var keepItems []triageItem
	var discardItems []triageItem

	for _, item := range allItems {
		if item.result.Saveable {
			keepItems = append(keepItems, item)
		} else {
			discardItems = append(discardItems, item)
		}
	}

	// Display triage report
	fmt.Println("============================================")
	fmt.Println("Triage Report")
	fmt.Println("============================================")
	fmt.Println()

	// KEEP section
	fmt.Printf("KEEP (%d items)\n", len(keepItems))
	fmt.Println("--------------------------------------------")
	if len(keepItems) == 0 {
		fmt.Println("   (none)")
	} else {
		for i, item := range keepItems {
			displayPath := filepath.Base(item.path)
			if relPath, err := filepath.Rel(dirPath, item.path); err == nil && relPath != displayPath {
				displayPath = relPath
			}
			fmt.Printf("   %2d. %s\n", i+1, displayPath)
			fmt.Printf("       %s\n", item.result.Reason)
		}
	}
	fmt.Println()

	// DISCARD section
	fmt.Printf("DISCARD (%d items)\n", len(discardItems))
	fmt.Println("--------------------------------------------")
	if len(discardItems) == 0 {
		fmt.Println("   (none)")
		fmt.Println()
		fmt.Println("All media files are worth keeping!")
		return
	}

	var totalDiscardSize int64
	for i, item := range discardItems {
		displayPath := filepath.Base(item.path)
		if relPath, err := filepath.Rel(dirPath, item.path); err == nil && relPath != displayPath {
			displayPath = relPath
		}

		// Get file size
		if info, err := os.Stat(item.path); err == nil {
			totalDiscardSize += info.Size()
		}

		fmt.Printf("   %2d. %s\n", i+1, displayPath)
		fmt.Printf("       %s\n", item.result.Reason)
	}
	fmt.Println()
	fmt.Printf("Total space to reclaim: %.1f MB\n", float64(totalDiscardSize)/(1024*1024))
	fmt.Println("============================================")
	fmt.Println()

	// Dry run: stop here
	if dryRunFlag {
		fmt.Println("Dry run complete. No files were deleted.")
		return
	}

	// Prompt for deletion confirmation
	fmt.Printf("Delete %d file(s)? This cannot be undone. (y/N): ", len(discardItems))

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read input, aborting deletion")
		fmt.Println("Aborted. No files were deleted.")
		return
	}

	input = strings.TrimSpace(strings.ToLower(input))
	if input != "y" && input != "yes" {
		fmt.Println("Aborted. No files were deleted.")
		return
	}

	// Delete discarded files
	fmt.Println()
	var deletedCount int
	var deleteErrors int
	for _, item := range discardItems {
		displayPath := filepath.Base(item.path)
		if relPath, err := filepath.Rel(dirPath, item.path); err == nil && relPath != displayPath {
			displayPath = relPath
		}

		if err := os.Remove(item.path); err != nil {
			log.Error().Err(err).Str("path", item.path).Msg("Failed to delete file")
			fmt.Printf("   FAILED: %s - %v\n", displayPath, err)
			deleteErrors++
		} else {
			fmt.Printf("   Deleted: %s\n", displayPath)
			deletedCount++
		}
	}

	fmt.Println()
	fmt.Printf("Deleted %d file(s)", deletedCount)
	if deleteErrors > 0 {
		fmt.Printf(", %d error(s)", deleteErrors)
	}
	fmt.Printf(", reclaimed %.1f MB\n", float64(totalDiscardSize)/(1024*1024))
}

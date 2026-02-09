package filehandler

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

// ScanOptions configures directory scanning behavior.
type ScanOptions struct {
	// MaxDepth limits recursion depth. 0 = unlimited, 1 = top-level only.
	MaxDepth int

	// Limit caps the number of images returned. 0 = unlimited.
	Limit int
}

// ScanDirectory scans a directory for supported image files and returns them as MediaFiles.
// Only images are scanned (videos are excluded for backward compatibility).
// Files are sorted alphabetically by filename.
// This is a convenience wrapper that calls ScanDirectoryWithOptions with default options.
func ScanDirectory(dirPath string) ([]*MediaFile, error) {
	return ScanDirectoryWithOptions(dirPath, ScanOptions{})
}

// ScanDirectoryMedia scans a directory for all supported media files (images AND videos).
// Files are sorted alphabetically by filename.
// This is a convenience wrapper that calls ScanDirectoryMediaWithOptions with default options.
func ScanDirectoryMedia(dirPath string) ([]*MediaFile, error) {
	return ScanDirectoryMediaWithOptions(dirPath, ScanOptions{})
}

// ScanDirectoryWithOptions scans a directory for supported image files with configurable options.
// Recursive scanning is enabled by default (MaxDepth=0 means unlimited).
// Symlinks to files are followed; symlinks to directories are skipped to prevent infinite loops.
// Files are sorted alphabetically by path for consistent ordering.
func ScanDirectoryWithOptions(dirPath string, opts ScanOptions) ([]*MediaFile, error) {
	log.Info().
		Str("path", dirPath).
		Int("max_depth", opts.MaxDepth).
		Int("limit", opts.Limit).
		Msg("Scanning directory for images")

	// Check if directory exists
	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("directory not found: %s", dirPath)
		}
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", dirPath)
	}

	// Convert to absolute path for consistent depth calculation
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	baseDepth := strings.Count(absPath, string(os.PathSeparator))

	var mediaFiles []*MediaFile
	limitReached := false

	// Walk the directory tree
	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Warn().Err(err).Str("path", path).Msg("Error accessing path, skipping")
			return nil // Continue walking despite errors
		}

		// Check depth limit
		if opts.MaxDepth > 0 {
			currentDepth := strings.Count(path, string(os.PathSeparator)) - baseDepth
			if d.IsDir() && currentDepth >= opts.MaxDepth {
				return fs.SkipDir
			}
		}

		// Skip directories (but continue into them)
		if d.IsDir() {
			return nil
		}

		// Handle symlinks: follow file symlinks, skip directory symlinks
		if d.Type()&fs.ModeSymlink != 0 {
			linkTarget, err := filepath.EvalSymlinks(path)
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to resolve symlink, skipping")
				return nil
			}

			targetInfo, err := os.Stat(linkTarget)
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to stat symlink target, skipping")
				return nil
			}

			if targetInfo.IsDir() {
				log.Debug().Str("path", path).Msg("Skipping symlink to directory")
				return nil
			}
			// Continue processing file symlinks
		}

		// Check if limit reached
		if opts.Limit > 0 && len(mediaFiles) >= opts.Limit {
			limitReached = true
			return fs.SkipAll
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))

		// Only process images
		if !IsImage(ext) {
			return nil
		}

		mediaFile, err := LoadMediaFile(path)
		if err != nil {
			log.Warn().Err(err).Str("file", d.Name()).Msg("Failed to load media file, skipping")
			return nil
		}

		mediaFiles = append(mediaFiles, mediaFile)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	// Sort by path for consistent ordering
	sort.Slice(mediaFiles, func(i, j int) bool {
		return mediaFiles[i].Path < mediaFiles[j].Path
	})

	logEvent := log.Info().
		Int("total_images", len(mediaFiles)).
		Str("directory", dirPath)

	if limitReached {
		logEvent.Bool("limit_reached", true)
	}

	logEvent.Msg("Directory scan complete")

	return mediaFiles, nil
}

// ScanDirectoryMediaWithOptions scans a directory for all supported media files (images AND videos)
// with configurable options. This is the mixed-media version of ScanDirectoryWithOptions.
// Recursive scanning is enabled by default (MaxDepth=0 means unlimited).
// Symlinks to files are followed; symlinks to directories are skipped to prevent infinite loops.
// Files are sorted alphabetically by path for consistent ordering.
func ScanDirectoryMediaWithOptions(dirPath string, opts ScanOptions) ([]*MediaFile, error) {
	log.Info().
		Str("path", dirPath).
		Int("max_depth", opts.MaxDepth).
		Int("limit", opts.Limit).
		Msg("Scanning directory for media (images + videos)")

	// Check if directory exists
	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("directory not found: %s", dirPath)
		}
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", dirPath)
	}

	// Convert to absolute path for consistent depth calculation
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	baseDepth := strings.Count(absPath, string(os.PathSeparator))

	var mediaFiles []*MediaFile
	var imageCount, videoCount int
	limitReached := false

	// Walk the directory tree
	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Warn().Err(err).Str("path", path).Msg("Error accessing path, skipping")
			return nil // Continue walking despite errors
		}

		// Check depth limit
		if opts.MaxDepth > 0 {
			currentDepth := strings.Count(path, string(os.PathSeparator)) - baseDepth
			if d.IsDir() && currentDepth >= opts.MaxDepth {
				return fs.SkipDir
			}
		}

		// Skip directories (but continue into them)
		if d.IsDir() {
			return nil
		}

		// Handle symlinks: follow file symlinks, skip directory symlinks
		if d.Type()&fs.ModeSymlink != 0 {
			linkTarget, err := filepath.EvalSymlinks(path)
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to resolve symlink, skipping")
				return nil
			}

			targetInfo, err := os.Stat(linkTarget)
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to stat symlink target, skipping")
				return nil
			}

			if targetInfo.IsDir() {
				log.Debug().Str("path", path).Msg("Skipping symlink to directory")
				return nil
			}
			// Continue processing file symlinks
		}

		// Check if limit reached
		if opts.Limit > 0 && len(mediaFiles) >= opts.Limit {
			limitReached = true
			return fs.SkipAll
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))

		// Process both images AND videos (mixed media)
		if !IsSupported(ext) {
			return nil
		}

		mediaFile, err := LoadMediaFile(path)
		if err != nil {
			log.Warn().Err(err).Str("file", d.Name()).Msg("Failed to load media file, skipping")
			return nil
		}

		// Track counts by type
		if IsImage(ext) {
			imageCount++
		} else if IsVideo(ext) {
			videoCount++
		}

		mediaFiles = append(mediaFiles, mediaFile)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	// Sort by path for consistent ordering
	sort.Slice(mediaFiles, func(i, j int) bool {
		return mediaFiles[i].Path < mediaFiles[j].Path
	})

	logEvent := log.Info().
		Int("total_media", len(mediaFiles)).
		Int("images", imageCount).
		Int("videos", videoCount).
		Str("directory", dirPath)

	if limitReached {
		logEvent.Bool("limit_reached", true)
	}

	logEvent.Msg("Directory media scan complete")

	return mediaFiles, nil
}

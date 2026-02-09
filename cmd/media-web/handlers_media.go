package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/rs/zerolog/log"
)

// GET /api/media/thumbnail?path=...
func handleThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Reject path traversal attempts (DDR-028 Problem 6)
	if containsPathTraversal(filePath) {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		httpError(w, http.StatusNotFound, "file not found")
		return
	}
	if info.IsDir() {
		httpError(w, http.StatusBadRequest, "path is a directory")
		return
	}

	ext := strings.ToLower(filepath.Ext(absPath))

	// For images, generate a smaller thumbnail for the web UI
	if mime, ok := filehandler.SupportedImageExtensions[ext]; ok {
		mf := &filehandler.MediaFile{
			Path:     absPath,
			MIMEType: mime,
			Size:     info.Size(),
		}
		thumbData, thumbMIME, err := filehandler.GenerateThumbnail(mf, 400)
		if err != nil {
			log.Warn().Err(err).Str("path", absPath).Msg("Failed to generate thumbnail")
			httpError(w, http.StatusInternalServerError, "thumbnail generation failed")
			return
		}
		w.Header().Set("Content-Type", thumbMIME)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(thumbData)
		return
	}

	// For videos, serve a placeholder SVG
	if _, ok := filehandler.SupportedVideoExtensions[ext]; ok {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400" viewBox="0 0 400 400">
			<rect width="400" height="400" fill="#1a1d27"/>
			<polygon points="160,120 160,280 290,200" fill="#8b8fa8"/>
			<text x="200" y="340" text-anchor="middle" fill="#8b8fa8" font-size="16" font-family="sans-serif">%s</text>
		</svg>`, filepath.Base(absPath))
		return
	}

	httpError(w, http.StatusBadRequest, "unsupported file type")
}

func handleFullImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		httpError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Reject path traversal attempts (DDR-028 Problem 6)
	if containsPathTraversal(filePath) {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		httpError(w, http.StatusNotFound, "file not found")
		return
	}
	if info.IsDir() {
		httpError(w, http.StatusBadRequest, "path is a directory")
		return
	}

	ext := strings.ToLower(filepath.Ext(absPath))

	// Only serve supported media types
	if _, ok := filehandler.SupportedImageExtensions[ext]; !ok {
		if _, ok := filehandler.SupportedVideoExtensions[ext]; !ok {
			httpError(w, http.StatusBadRequest, "unsupported file type")
			return
		}
	}

	// Serve the original file directly — http.ServeFile handles Content-Type,
	// range requests, and caching headers automatically.
	http.ServeFile(w, r, absPath)
}

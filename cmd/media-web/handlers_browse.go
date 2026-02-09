package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/ncruces/zenity"
	"github.com/rs/zerolog/log"
)

// GET /api/browse?path=...
func handleBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			httpError(w, http.StatusInternalServerError, "cannot determine home directory")
			return
		}
		dirPath = home
	}

	// Reject path traversal attempts (DDR-028 Problem 6)
	if containsPathTraversal(dirPath) {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}

	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			httpError(w, http.StatusNotFound, "path not found")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !info.IsDir() {
		httpError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "cannot read directory")
		return
	}

	type fileEntry struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		IsDir    bool   `json:"isDir"`
		Size     int64  `json:"size"`
		MIMEType string `json:"mimeType"`
	}

	entries := make([]fileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}

		entryPath := filepath.Join(absPath, de.Name())
		fi, err := de.Info()
		if err != nil {
			continue
		}

		entry := fileEntry{
			Name:  de.Name(),
			Path:  entryPath,
			IsDir: de.IsDir(),
			Size:  fi.Size(),
		}

		if !de.IsDir() {
			ext := strings.ToLower(filepath.Ext(de.Name()))
			if mime, ok := filehandler.SupportedImageExtensions[ext]; ok {
				entry.MIMEType = mime
			} else if mime, ok := filehandler.SupportedVideoExtensions[ext]; ok {
				entry.MIMEType = mime
			}
		}

		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	parent := filepath.Dir(absPath)
	if parent == absPath {
		parent = ""
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"path":    absPath,
		"parent":  parent,
		"entries": entries,
	})
}

// POST /api/pick
// Opens a native OS file/directory picker dialog and returns selected paths.
func handlePick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Mode string `json:"mode"` // "files" or "directory"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var paths []string

	switch req.Mode {
	case "files":
		selected, err := zenity.SelectFileMultiple(
			zenity.Title("Select media files"),
			zenity.FileFilters{
				{
					Name: "Media files",
					Patterns: []string{
						"*.jpg", "*.jpeg", "*.png", "*.gif", "*.webp",
						"*.heic", "*.heif",
						"*.mp4", "*.mov", "*.avi", "*.webm", "*.mkv",
					},
				},
			},
		)
		if err != nil {
			if errors.Is(err, zenity.ErrCanceled) {
				respondJSON(w, http.StatusOK, map[string]interface{}{
					"paths":    []string{},
					"canceled": true,
				})
				return
			}
			log.Error().Err(err).Msg("File picker failed")
			httpError(w, http.StatusInternalServerError, "file picker failed")
			return
		}
		paths = selected

	case "directory":
		selected, err := zenity.SelectFile(
			zenity.Directory(),
			zenity.Title("Select folder"),
		)
		if err != nil {
			if errors.Is(err, zenity.ErrCanceled) {
				respondJSON(w, http.StatusOK, map[string]interface{}{
					"paths":    []string{},
					"canceled": true,
				})
				return
			}
			log.Error().Err(err).Msg("Directory picker failed")
			httpError(w, http.StatusInternalServerError, "directory picker failed")
			return
		}
		paths = []string{selected}

	default:
		httpError(w, http.StatusBadRequest, "mode must be 'files' or 'directory'")
		return
	}

	log.Info().Str("mode", req.Mode).Int("count", len(paths)).Msg("Files picked via native dialog")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"paths":    paths,
		"canceled": false,
	})
}

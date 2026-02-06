package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fpang/gemini-media-cli/internal/auth"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/google/generative-ai-go/genai"
	"github.com/ncruces/zenity"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"google.golang.org/api/option"
)

//go:embed all:frontend_dist
var frontendFS embed.FS

// CLI flags
var (
	portFlag  int
	modelFlag string
)

var rootCmd = &cobra.Command{
	Use:   "media-web",
	Short: "Web UI for media triage and selection",
	Long: `Media Web starts a local web server that provides a visual interface
for triaging and selecting media files. Browse directories, view thumbnails,
and confirm actions through your browser.

Examples:
  media-web
  media-web --port 9090
  media-web --model gemini-3-pro-preview`,
	Run: runMain,
}

func init() {
	rootCmd.Flags().IntVar(&portFlag, "port", 8080, "Port to listen on")
	rootCmd.Flags().StringVarP(&modelFlag, "model", "m", chat.DefaultModelName, "Gemini model to use")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- Triage Job Management ---

type triageJob struct {
	mu      sync.Mutex
	id      string
	status  string // "pending", "processing", "complete", "error"
	keep    []triageResultItem
	discard []triageResultItem
	errMsg  string
	paths   []string // original input paths
}

type triageResultItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Path         string `json:"path"`
	Saveable     bool   `json:"saveable"`
	Reason       string `json:"reason"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

var (
	jobsMu sync.Mutex
	jobs   = make(map[string]*triageJob)
	jobSeq int
)

func newJob(paths []string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	jobSeq++
	id := fmt.Sprintf("triage-%d", jobSeq)
	j := &triageJob{
		id:     id,
		status: "pending",
		paths:  paths,
	}
	jobs[id] = j
	return j
}

func getJob(id string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	return jobs[id]
}

// --- Main Server ---

func runMain(cmd *cobra.Command, args []string) {
	logging.Init()

	// Validate API key at startup
	apiKey, err := auth.GetAPIKey()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get API key")
	}

	ctx := context.Background()
	validationClient, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Gemini client for validation")
	}
	if err := auth.ValidateAPIKey(ctx, validationClient); err != nil {
		validationClient.Close()
		log.Fatal().Err(err).Msg("Invalid API key")
	}
	validationClient.Close()
	log.Info().Msg("API key validated")

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/browse", handleBrowse)
	mux.HandleFunc("/api/pick", handlePick)
	mux.HandleFunc("/api/triage/start", handleTriageStart)
	mux.HandleFunc("/api/triage/", handleTriageRoutes)
	mux.HandleFunc("/api/media/thumbnail", handleThumbnail)

	// Frontend static files (SPA fallback)
	frontendSub, err := fs.Sub(frontendFS, "frontend_dist")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to access embedded frontend")
	}
	fileServer := http.FileServer(http.FS(frontendSub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Security headers
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; style-src 'self' 'unsafe-inline'; connect-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// SPA fallback: if the file doesn't exist, serve index.html
		path := r.URL.Path
		if path != "/" {
			f, err := frontendSub.Open(strings.TrimPrefix(path, "/"))
			if err != nil {
				// File not found — serve index.html for client-side routing
				r.URL.Path = "/"
			} else {
				f.Close()
			}
		}
		fileServer.ServeHTTP(w, r)
	})

	// Wrap with logging and CORS for local dev
	handler := withLogging(withCORS(mux))

	addr := fmt.Sprintf(":%d", portFlag)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Info().Msg("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Info().Int("port", portFlag).Msg("Starting web server")
	fmt.Printf("\n  Media Web UI: http://localhost:%d\n\n", portFlag)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err).Msg("Server failed")
	}
}

// --- Middleware ---

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Dur("duration", time.Since(start)).
				Msg("API request")
		}
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only allow localhost origins for Phase 1
		origin := r.Header.Get("Origin")
		if origin != "" && (strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:")) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- API Handlers ---

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

// POST /api/triage/start
func handleTriageStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Paths []string `json:"paths"`
		Model string   `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Paths) == 0 {
		httpError(w, http.StatusBadRequest, "no paths provided")
		return
	}

	model := modelFlag
	if req.Model != "" {
		model = req.Model
	}

	job := newJob(req.Paths)

	go runTriageJob(job, model)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

// Routes under /api/triage/{id}/...
func handleTriageRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/triage/"), "/")
	if len(parts) < 2 {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	jobID := parts[0]
	// The job IDs are stored as "triage-N", and the URL path is /api/triage/{N}/...
	// so we need to reconstruct the full ID
	if !strings.HasPrefix(jobID, "triage-") {
		jobID = "triage-" + jobID
	}
	action := parts[1]

	job := getJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "job not found")
		return
	}

	switch action {
	case "results":
		handleTriageResults(w, r, job)
	case "confirm":
		handleTriageConfirm(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/triage/{id}/results
func handleTriageResults(w http.ResponseWriter, r *http.Request, job *triageJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":      job.id,
		"status":  job.status,
		"keep":    job.keep,
		"discard": job.discard,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}

	respondJSON(w, http.StatusOK, resp)
}

// POST /api/triage/{id}/confirm
func handleTriageConfirm(w http.ResponseWriter, r *http.Request, job *triageJob) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		DeletePaths []string `json:"deletePaths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var (
		deleted       int
		errMsgs       = make([]string, 0)
		reclaimedSize int64
	)

	for _, p := range req.DeletePaths {
		if !isValidDeletePath(job, p) {
			errMsgs = append(errMsgs, fmt.Sprintf("path not in triage results: %s", p))
			continue
		}

		info, err := os.Stat(p)
		if err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("cannot stat %s: %v", p, err))
			continue
		}
		size := info.Size()

		if err := os.Remove(p); err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("failed to delete %s: %v", p, err))
			continue
		}

		deleted++
		reclaimedSize += size
		log.Info().Str("path", p).Msg("Deleted file")
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"errors":         errMsgs,
		"reclaimedBytes": reclaimedSize,
	})
}

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

// --- Triage Processing ---

// runTriageJob uses the existing AskMediaTriage function from the chat package,
// matching the same pattern as the media-triage CLI.
func runTriageJob(job *triageJob, model string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	apiKey, err := auth.GetAPIKey()
	if err != nil {
		setJobError(job, fmt.Sprintf("API key error: %v", err))
		return
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		setJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}
	defer client.Close()

	// Collect all media files from the provided paths
	var allMediaFiles []*filehandler.MediaFile
	for _, p := range job.paths {
		info, err := os.Stat(p)
		if err != nil {
			log.Warn().Err(err).Str("path", p).Msg("Skipping inaccessible path")
			continue
		}
		if info.IsDir() {
			files, err := filehandler.ScanDirectoryMediaWithOptions(p, filehandler.ScanOptions{})
			if err != nil {
				log.Warn().Err(err).Str("path", p).Msg("Failed to scan directory")
				continue
			}
			allMediaFiles = append(allMediaFiles, files...)
		} else {
			mf, err := filehandler.LoadMediaFile(p)
			if err != nil {
				log.Warn().Err(err).Str("path", p).Msg("Failed to load media file")
				continue
			}
			allMediaFiles = append(allMediaFiles, mf)
		}
	}

	if len(allMediaFiles) == 0 {
		setJobError(job, "No media files found in the provided paths")
		return
	}

	log.Info().Int("count", len(allMediaFiles)).Msg("Starting web triage evaluation")

	// Pre-filter short videos (same logic as media-triage CLI)
	var mediaForAI []*filehandler.MediaFile
	for _, mf := range allMediaFiles {
		if mf.Metadata != nil && mf.Metadata.GetMediaType() == "video" {
			if vm, ok := mf.Metadata.(*filehandler.VideoMetadata); ok && vm.Duration < 2.0 {
				job.mu.Lock()
				job.discard = append(job.discard, triageResultItem{
					Media:        0,
					Filename:     filepath.Base(mf.Path),
					Path:         mf.Path,
					Saveable:     false,
					Reason:       "Video too short — likely accidental recording",
					ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?path=%s", mf.Path),
				})
				job.mu.Unlock()
				continue
			}
		}
		mediaForAI = append(mediaForAI, mf)
	}

	if len(mediaForAI) == 0 {
		// All files were pre-filtered
		job.mu.Lock()
		job.status = "complete"
		job.mu.Unlock()
		return
	}

	// Use the existing AskMediaTriage function from the chat package
	triageResults, err := chat.AskMediaTriage(ctx, client, mediaForAI, model)
	if err != nil {
		setJobError(job, fmt.Sprintf("Triage failed: %v", err))
		return
	}

	// Map results to items with paths and thumbnail URLs
	job.mu.Lock()
	for _, tr := range triageResults {
		idx := tr.Media - 1
		if idx < 0 || idx >= len(mediaForAI) {
			continue
		}
		mf := mediaForAI[idx]
		item := triageResultItem{
			Media:        tr.Media,
			Filename:     tr.Filename,
			Path:         mf.Path,
			Saveable:     tr.Saveable,
			Reason:       tr.Reason,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?path=%s", mf.Path),
		}
		if tr.Saveable {
			job.keep = append(job.keep, item)
		} else {
			job.discard = append(job.discard, item)
		}
	}
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("keep", len(job.keep)).
		Int("discard", len(job.discard)).
		Msg("Web triage complete")
}

func setJobError(job *triageJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Triage job failed")
}

func isValidDeletePath(job *triageJob, path string) bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	for _, item := range job.discard {
		if item.Path == path {
			return true
		}
	}
	return false
}

// --- JSON Helpers ---

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func httpError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

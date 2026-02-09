package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fpang/gemini-media-cli/internal/auth"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
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

func runMain(cmd *cobra.Command, args []string) {
	logging.Init()

	// Validate API key at startup
	apiKey, err := auth.GetAPIKey()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get API key")
	}

	ctx := context.Background()
	validationClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create Gemini client for validation")
	}
	if err := auth.ValidateAPIKey(ctx, validationClient); err != nil {
		log.Fatal().Err(err).Msg("Invalid API key")
	}
	log.Info().Msg("API key validated")

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/browse", handleBrowse)
	mux.HandleFunc("/api/pick", handlePick)
	mux.HandleFunc("/api/triage/start", handleTriageStart)
	mux.HandleFunc("/api/triage/", handleTriageRoutes)
	mux.HandleFunc("/api/media/thumbnail", handleThumbnail)
	mux.HandleFunc("/api/media/full", handleFullImage)

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

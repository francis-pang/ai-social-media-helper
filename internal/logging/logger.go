package logging

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Init initializes the global logger with configuration from environment variables.
// GEMINI_LOG_LEVEL controls the log level: trace, debug, info, warn, error (default: debug).
//
// In Lambda environments (detected via AWS_LAMBDA_FUNCTION_NAME), output is JSON for
// CloudWatch ingestion. In local/CLI environments, output uses human-readable console format.
func Init() {
	level := os.Getenv("GEMINI_LOG_LEVEL")
	switch level {
	case "trace":
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		// Default to debug — this is the only environment (no staging/dev).
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	// Use JSON output in Lambda for CloudWatch; console writer for local/CLI.
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	log.Info().Str("level", zerolog.GlobalLevel().String()).Msg("Logger initialized")
}

// WithLambdaContext returns a sub-logger enriched with Lambda request ID and function name
// extracted from the Lambda context. If the context does not contain Lambda metadata,
// the global logger is returned unchanged.
func WithLambdaContext(ctx context.Context) zerolog.Logger {
	if lc, ok := lambdacontext.FromContext(ctx); ok {
		return log.With().
			Str("requestId", lc.AwsRequestID).
			Str("functionName", lambdacontext.FunctionName).
			Logger()
	}
	return log.Logger
}

// WithJob returns a sub-logger enriched with sessionId and jobId fields for
// consistent correlation across all log lines within a single job.
func WithJob(sessionId, jobId string) zerolog.Logger {
	return log.With().
		Str("sessionId", sessionId).
		Str("jobId", jobId).
		Logger()
}

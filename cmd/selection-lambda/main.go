// Package main provides a Lambda entry point for AI-powered media selection.
//
// This Lambda is invoked by the Step Functions SelectionPipeline after all
// thumbnails are generated (fan-in). It downloads all media files and
// thumbnails, sends them to Gemini for structured JSON selection analysis,
// and writes the complete results to DynamoDB.
//
// Container: Heavy (Dockerfile.heavy — includes ffmpeg for video compression)
// Memory: 4 GB
// Timeout: 15 minutes
//
// See DDR-035: Multi-Lambda Deployment Architecture
// See DDR-043: Step Functions Lambda Entrypoints
package main

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// AWS clients and configuration initialized at cold start.
var (
	s3Client      *s3.Client
	presignClient *s3.PresignClient
	sessionStore  store.SessionStore
	mediaBucket   string
	ebClient      *eventbridge.Client
	lambdaClient  *lambdasvc.Client
	ragQueryArn   string
)

var coldStart = true

func init() {
	initStart := time.Now()
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}
	log.Debug().Str("region", cfg.Region).Msg("AWS config loaded")

	s3Client = s3.NewFromConfig(cfg)
	presignClient = s3.NewPresignClient(s3Client)
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	// Initialize DynamoDB store.
	tableName := os.Getenv("DYNAMO_TABLE_NAME")
	if tableName == "" {
		tableName = "media-selection-sessions"
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	sessionStore = store.NewDynamoStore(ddbClient, tableName)

	// Load Gemini API key from SSM Parameter Store if not set.
	if os.Getenv("GEMINI_API_KEY") == "" {
		ssmClient := ssm.NewFromConfig(cfg)
		paramName := os.Getenv("SSM_API_KEY_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/gemini-api-key"
		}
		ssmStart := time.Now()
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
		}
		os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
		log.Debug().Str("param", paramName).Dur("elapsed", time.Since(ssmStart)).Msg("Gemini API key loaded from SSM")
	}

	ebClient = eventbridge.NewFromConfig(cfg)
	lambdaClient = lambdasvc.NewFromConfig(cfg)
	ragQueryArn = os.Getenv("RAG_QUERY_LAMBDA_ARN")
	if ragQueryArn == "" {
		paramPath := os.Getenv("RAG_QUERY_LAMBDA_ARN_PARAM")
		if paramPath != "" {
			ssmClient := ssm.NewFromConfig(cfg)
			result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
				Name:           aws.String(paramPath),
				WithDecryption: aws.Bool(false),
			})
			if err == nil && result.Parameter != nil && result.Parameter.Value != nil {
				ragQueryArn = *result.Parameter.Value
				log.Debug().Str("param", paramPath).Msg("RAG Query Lambda ARN loaded from SSM")
			}
		}
	}

	// Emit consolidated cold-start log for troubleshooting.
	logging.NewStartupLogger("selection-lambda").
		InitDuration(time.Since(initStart)).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", tableName).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		Log()
}

func main() {
	lambda.Start(handler)
}
